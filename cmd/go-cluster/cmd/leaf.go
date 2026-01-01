package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/spf13/cobra"
)

var leafCmd = &cobra.Command{
	Use:   "leaf",
	Short: "Manage leaf node connections",
	Long:  `Commands for managing NATS leaf node connections for multi-zone deployments.`,
}

var leafListCmd = &cobra.Command{
	Use:   "list",
	Short: "List leaf connections",
	RunE:  runLeafList,
}

var leafStatusCmd = &cobra.Command{
	Use:   "status <platform>",
	Short: "Show leaf connection status",
	Args:  cobra.ExactArgs(1),
	RunE:  runLeafStatus,
}

var leafPromoteCmd = &cobra.Command{
	Use:   "promote <platform>",
	Short: "Promote leaf platform to hub",
	Args:  cobra.ExactArgs(1),
	RunE:  runLeafPromote,
}

var leafPingCmd = &cobra.Command{
	Use:   "ping <platform>",
	Short: "Ping a leaf platform",
	Args:  cobra.ExactArgs(1),
	RunE:  runLeafPing,
}

func init() {
	rootCmd.AddCommand(leafCmd)
	leafCmd.AddCommand(leafListCmd)
	leafCmd.AddCommand(leafStatusCmd)
	leafCmd.AddCommand(leafPromoteCmd)
	leafCmd.AddCommand(leafPingCmd)

	// Promote flags
	leafPromoteCmd.Flags().String("reason", "", "Reason for promotion (e.g., hub-maintenance)")
	leafPromoteCmd.Flags().Bool("force", false, "Force promotion even without quorum")
}

// LeafRecord represents a leaf connection record
type LeafRecord struct {
	Platform    string    `json:"platform"`
	Zone        string    `json:"zone,omitempty"`
	Status      string    `json:"status"`
	LatencyMs   int64     `json:"latency_ms"`
	ConnectedAt time.Time `json:"connected_at"`
	LastSeen    time.Time `json:"last_seen"`
	Apps        []string  `json:"apps,omitempty"`
}

func runLeafList(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
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

	// Try to access leaves bucket
	bucketName := fmt.Sprintf("%s_leaves", platformName)
	kv, err := js.KeyValue(ctx, bucketName)
	if err != nil {
		fmt.Println("No leaf connections found")
		fmt.Println("(This platform may not be configured as a hub)")
		return nil
	}

	keys, err := kv.Keys(ctx)
	if err != nil {
		if err.Error() == "nats: no keys found" {
			fmt.Println("No leaf connections")
			return nil
		}
		return fmt.Errorf("failed to list leaves: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PLATFORM\tZONE\tSTATUS\tLATENCY\tLAST SEEN")

	for _, key := range keys {
		entry, err := kv.Get(ctx, key)
		if err != nil {
			continue
		}

		var leaf LeafRecord
		if err := json.Unmarshal(entry.Value(), &leaf); err != nil {
			continue
		}

		latency := fmt.Sprintf("%dms", leaf.LatencyMs)
		lastSeen := time.Since(leaf.LastSeen).Round(time.Second).String()

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s ago\n",
			leaf.Platform,
			leaf.Zone,
			leaf.Status,
			latency,
			lastSeen,
		)
	}

	return w.Flush()
}

func runLeafStatus(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	leafPlatform := args[0]

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

	bucketName := fmt.Sprintf("%s_leaves", platformName)
	kv, err := js.KeyValue(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("leaf information not available")
	}

	entry, err := kv.Get(ctx, leafPlatform)
	if err != nil {
		return fmt.Errorf("leaf platform %q not found", leafPlatform)
	}

	var leaf LeafRecord
	if err := json.Unmarshal(entry.Value(), &leaf); err != nil {
		return fmt.Errorf("failed to parse leaf data: %w", err)
	}

	fmt.Printf("Platform:     %s\n", leaf.Platform)
	fmt.Printf("Zone:         %s\n", leaf.Zone)
	fmt.Printf("Hub:          %s\n", platformName)
	fmt.Printf("Status:       %s\n", leaf.Status)
	fmt.Printf("Latency:      %dms\n", leaf.LatencyMs)
	fmt.Printf("Connected:    %s\n", leaf.ConnectedAt.Format(time.RFC3339))
	fmt.Printf("Last Seen:    %s (%s ago)\n", leaf.LastSeen.Format(time.RFC3339), time.Since(leaf.LastSeen).Round(time.Second))
	if len(leaf.Apps) > 0 {
		fmt.Printf("Apps:         %v\n", leaf.Apps)
	}

	return nil
}

func runLeafPromote(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	leafPlatform := args[0]
	reason, _ := cmd.Flags().GetString("reason")
	force, _ := cmd.Flags().GetBool("force")

	fmt.Printf("Promoting leaf platform %s to hub role...\n", leafPlatform)
	if reason != "" {
		fmt.Printf("Reason: %s\n", reason)
	}
	if force {
		fmt.Println("(Force mode - bypassing quorum check)")
	}

	fmt.Println("\nNote: Leaf promotion requires connection to running platform.")
	fmt.Println("This operation should be performed via the go-cluster library API:")
	fmt.Println("")
	fmt.Println("  // On the leaf platform that will become hub:")
	fmt.Println("  platform.LeafManager().PromoteToHub(ctx)")
	fmt.Println("")
	fmt.Println("  // Or from the hub to trigger promotion:")
	fmt.Println("  platform.LeafManager().PromoteLeaf(ctx, leafPlatform)")

	return nil
}

func runLeafPing(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	leafPlatform := args[0]

	// Attempt to measure latency via NATS
	nc, err := nats.Connect(getNATSURL())
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	defer nc.Close()

	fmt.Printf("Pinging leaf platform %s...\n\n", leafPlatform)

	// Try to ping via NATS request
	subject := fmt.Sprintf("%s._.ping", leafPlatform)

	for i := 0; i < 3; i++ {
		start := time.Now()
		_, err := nc.Request(subject, []byte("ping"), 5*time.Second)
		rtt := time.Since(start)

		if err != nil {
			fmt.Printf("  seq=%d: timeout or error\n", i+1)
		} else {
			fmt.Printf("  seq=%d: rtt=%v\n", i+1, rtt.Round(time.Microsecond))
		}

		if i < 2 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	return nil
}
