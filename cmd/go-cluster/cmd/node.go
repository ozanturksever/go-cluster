package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/spf13/cobra"
)

var nodeCmd = &cobra.Command{
	Use:   "node",
	Short: "Manage cluster nodes",
	Long:  `Commands for managing nodes in the cluster.`,
}

var nodeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List nodes in the platform",
	RunE:  runNodeList,
}

var nodeStatusCmd = &cobra.Command{
	Use:   "status <node-id>",
	Short: "Show detailed status of a node",
	Args:  cobra.ExactArgs(1),
	RunE:  runNodeStatus,
}

var nodeDrainCmd = &cobra.Command{
	Use:   "drain <node-id>",
	Short: "Drain a node (move all apps away)",
	Args:  cobra.ExactArgs(1),
	RunE:  runNodeDrain,
}

var nodeLabelCmd = &cobra.Command{
	Use:   "label <node-id> <key=value>...",
	Short: "Set labels on a node",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runNodeLabel,
}

var nodeCordonCmd = &cobra.Command{
	Use:   "cordon <node-id>",
	Short: "Mark node as unschedulable",
	Args:  cobra.ExactArgs(1),
	RunE:  runNodeCordon,
}

var nodeUncordonCmd = &cobra.Command{
	Use:   "uncordon <node-id>",
	Short: "Mark node as schedulable",
	Args:  cobra.ExactArgs(1),
	RunE:  runNodeUncordon,
}

func init() {
	rootCmd.AddCommand(nodeCmd)
	nodeCmd.AddCommand(nodeListCmd)
	nodeCmd.AddCommand(nodeStatusCmd)
	nodeCmd.AddCommand(nodeDrainCmd)
	nodeCmd.AddCommand(nodeLabelCmd)
	nodeCmd.AddCommand(nodeCordonCmd)
	nodeCmd.AddCommand(nodeUncordonCmd)

	// Drain flags
	nodeDrainCmd.Flags().Duration("timeout", 5*time.Minute, "Drain timeout")
	nodeDrainCmd.Flags().Bool("force", false, "Force drain even if apps cannot be moved")
}

// MemberRecord represents a node in the cluster (matches membership.go)
type MemberRecord struct {
	NodeID    string            `json:"node_id"`
	Platform  string            `json:"platform"`
	Address   string            `json:"address"`
	Labels    map[string]string `json:"labels"`
	JoinedAt  time.Time         `json:"joined_at"`
	LastSeen  time.Time         `json:"last_seen"`
	Apps      []string          `json:"apps"`
	IsHealthy bool              `json:"is_healthy"`
}

func runNodeList(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required (use --platform or set PLATFORM env)")
	}

	nc, err := nats.Connect(getNATSURL())
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("failed to create JetStream context: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bucketName := fmt.Sprintf("%s_members", platformName)
	kv, err := js.KeyValue(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("failed to access members bucket: %w", err)
	}

	keys, err := kv.Keys(ctx)
	if err != nil {
		if err.Error() == "nats: no keys found" {
			fmt.Println("No nodes found in platform")
			return nil
		}
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NODE ID\tADDRESS\tSTATUS\tAPPS\tLABELS\tLAST SEEN")

	for _, key := range keys {
		entry, err := kv.Get(ctx, key)
		if err != nil {
			continue
		}

		var member MemberRecord
		if err := json.Unmarshal(entry.Value(), &member); err != nil {
			continue
		}

		status := "healthy"
		if !member.IsHealthy {
			status = "unhealthy"
		}
		if time.Since(member.LastSeen) > 30*time.Second {
			status = "unknown"
		}

		labels := formatLabels(member.Labels)
		apps := strings.Join(member.Apps, ",")
		if apps == "" {
			apps = "-"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			member.NodeID,
			member.Address,
			status,
			apps,
			labels,
			member.LastSeen.Format(time.RFC3339),
		)
	}

	return w.Flush()
}

func runNodeStatus(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	nodeID := args[0]

	nc, err := nats.Connect(getNATSURL())
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("failed to create JetStream context: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bucketName := fmt.Sprintf("%s_members", platformName)
	kv, err := js.KeyValue(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("failed to access members bucket: %w", err)
	}

	entry, err := kv.Get(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("node %q not found: %w", nodeID, err)
	}

	var member MemberRecord
	if err := json.Unmarshal(entry.Value(), &member); err != nil {
		return fmt.Errorf("failed to parse node data: %w", err)
	}

	fmt.Printf("Node: %s\n", member.NodeID)
	fmt.Printf("Platform: %s\n", member.Platform)
	fmt.Printf("Address: %s\n", member.Address)
	fmt.Printf("Status: %s\n", statusString(member))
	fmt.Printf("Joined: %s\n", member.JoinedAt.Format(time.RFC3339))
	fmt.Printf("Last Seen: %s (%s ago)\n", member.LastSeen.Format(time.RFC3339), time.Since(member.LastSeen).Round(time.Second))
	fmt.Printf("Apps: %s\n", strings.Join(member.Apps, ", "))
	fmt.Printf("Labels:\n")
	for k, v := range member.Labels {
		fmt.Printf("  %s: %s\n", k, v)
	}

	return nil
}

func runNodeDrain(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	nodeID := args[0]
	timeout, _ := cmd.Flags().GetDuration("timeout")
	force, _ := cmd.Flags().GetBool("force")

	fmt.Printf("Draining node %s (timeout: %s, force: %v)...\n", nodeID, timeout, force)
	fmt.Println("Note: Full drain implementation requires connection to running platform")
	fmt.Println("Use the go-cluster library API for programmatic drain operations")

	return nil
}

func runNodeLabel(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	nodeID := args[0]
	labelArgs := args[1:]

	labels := make(map[string]string)
	for _, arg := range labelArgs {
		parts := strings.SplitN(arg, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid label format %q, expected key=value", arg)
		}
		labels[parts[0]] = parts[1]
	}

	fmt.Printf("Setting labels on node %s:\n", nodeID)
	for k, v := range labels {
		fmt.Printf("  %s=%s\n", k, v)
	}
	fmt.Println("Note: Label updates require direct node access or platform API")

	return nil
}

func runNodeCordon(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	nodeID := args[0]
	fmt.Printf("Cordoning node %s (marking as unschedulable)...\n", nodeID)
	fmt.Println("Note: Cordon operations require platform API access")

	return nil
}

func runNodeUncordon(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	nodeID := args[0]
	fmt.Printf("Uncordoning node %s (marking as schedulable)...\n", nodeID)
	fmt.Println("Note: Uncordon operations require platform API access")

	return nil
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, ",")
}

func statusString(m MemberRecord) string {
	if time.Since(m.LastSeen) > 30*time.Second {
		return "unknown"
	}
	if m.IsHealthy {
		return "healthy"
	}
	return "unhealthy"
}
