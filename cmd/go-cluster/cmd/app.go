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

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Manage cluster apps",
	Long:  `Commands for managing applications in the cluster.`,
}

var appListCmd = &cobra.Command{
	Use:   "list",
	Short: "List apps in the platform",
	RunE:  runAppList,
}

var appStatusCmd = &cobra.Command{
	Use:   "status <app-name>",
	Short: "Show detailed status of an app",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppStatus,
}

var appMoveCmd = &cobra.Command{
	Use:   "move <app-name>",
	Short: "Move an app to another node",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppMove,
}

var appRebalanceCmd = &cobra.Command{
	Use:   "rebalance <app-name>",
	Short: "Rebalance app instances across nodes",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppRebalance,
}

func init() {
	rootCmd.AddCommand(appCmd)
	appCmd.AddCommand(appListCmd)
	appCmd.AddCommand(appStatusCmd)
	appCmd.AddCommand(appMoveCmd)
	appCmd.AddCommand(appRebalanceCmd)

	// Move flags
	appMoveCmd.Flags().String("from", "", "Source node ID")
	appMoveCmd.Flags().String("to", "", "Target node ID (optional, auto-select if not specified)")
	appMoveCmd.Flags().Duration("timeout", 5*time.Minute, "Migration timeout")

	// Rebalance flags
	appRebalanceCmd.Flags().Bool("dry-run", false, "Show what would be done without executing")
}

// AppRecord represents an app registration in the cluster
type AppRecord struct {
	Name      string   `json:"name"`
	Mode      string   `json:"mode"`
	Replicas  int      `json:"replicas,omitempty"`
	Leader    string   `json:"leader,omitempty"`
	Instances []string `json:"instances"`
}

// ElectionRecord represents election state
type ElectionRecord struct {
	Leader    string    `json:"leader"`
	Epoch     uint64    `json:"epoch"`
	Timestamp time.Time `json:"timestamp"`
}

func runAppList(cmd *cobra.Command, args []string) error {
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

	// Get apps from members data
	membersBucket := fmt.Sprintf("%s_members", platformName)
	membersKV, err := js.KeyValue(ctx, membersBucket)
	if err != nil {
		return fmt.Errorf("failed to access members bucket: %w", err)
	}

	keys, err := membersKV.Keys(ctx)
	if err != nil {
		if err.Error() == "nats: no keys found" {
			fmt.Println("No apps found in platform")
			return nil
		}
		return fmt.Errorf("failed to list members: %w", err)
	}

	// Aggregate apps across all nodes
	apps := make(map[string][]string) // app name -> list of nodes
	for _, key := range keys {
		entry, err := membersKV.Get(ctx, key)
		if err != nil {
			continue
		}

		var member MemberRecord
		if err := json.Unmarshal(entry.Value(), &member); err != nil {
			continue
		}

		for _, app := range member.Apps {
			apps[app] = append(apps[app], member.NodeID)
		}
	}

	if len(apps) == 0 {
		fmt.Println("No apps found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "APP\tINSTANCES\tLEADER\tNODES")

	for appName, nodes := range apps {
		leader := "-"
		// Try to get leader from election bucket
		electionBucket := fmt.Sprintf("%s_%s_election", platformName, appName)
		electionKV, err := js.KeyValue(ctx, electionBucket)
		if err == nil {
			if entry, err := electionKV.Get(ctx, "leader"); err == nil {
				var election ElectionRecord
				if json.Unmarshal(entry.Value(), &election) == nil {
					leader = election.Leader
				}
			}
		}

		fmt.Fprintf(w, "%s\t%d\t%s\t%s\n",
			appName,
			len(nodes),
			leader,
			strings.Join(nodes, ","),
		)
	}

	return w.Flush()
}

func runAppStatus(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	appName := args[0]

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

	fmt.Printf("App: %s\n", appName)
	fmt.Printf("Platform: %s\n\n", platformName)

	// Get election info
	electionBucket := fmt.Sprintf("%s_%s_election", platformName, appName)
	electionKV, err := js.KeyValue(ctx, electionBucket)
	if err == nil {
		if entry, err := electionKV.Get(ctx, "leader"); err == nil {
			var election ElectionRecord
			if json.Unmarshal(entry.Value(), &election) == nil {
				fmt.Printf("Leader: %s\n", election.Leader)
				fmt.Printf("Epoch: %d\n", election.Epoch)
				fmt.Printf("Last Election: %s\n", election.Timestamp.Format(time.RFC3339))
			}
		}
	} else {
		fmt.Printf("Leader: (no election data)\n")
	}

	// Get instances from members
	membersBucket := fmt.Sprintf("%s_members", platformName)
	membersKV, err := js.KeyValue(ctx, membersBucket)
	if err != nil {
		return fmt.Errorf("failed to access members bucket: %w", err)
	}

	keys, _ := membersKV.Keys(ctx)
	fmt.Printf("\nInstances:\n")

	for _, key := range keys {
		entry, err := membersKV.Get(ctx, key)
		if err != nil {
			continue
		}

		var member MemberRecord
		if err := json.Unmarshal(entry.Value(), &member); err != nil {
			continue
		}

		for _, app := range member.Apps {
			if app == appName {
				status := "healthy"
				if !member.IsHealthy {
					status = "unhealthy"
				}
				if time.Since(member.LastSeen) > 30*time.Second {
					status = "unknown"
				}
				fmt.Printf("  - %s (%s) - %s\n", member.NodeID, member.Address, status)
			}
		}
	}

	// Get services for this app
	servicesBucket := fmt.Sprintf("%s_services", platformName)
	servicesKV, err := js.KeyValue(ctx, servicesBucket)
	if err == nil {
		svcKeys, err := servicesKV.Keys(ctx)
		if err == nil {
			fmt.Printf("\nServices:\n")
			for _, key := range svcKeys {
				if !strings.HasPrefix(key, appName+".") {
					continue
				}
				entry, err := servicesKV.Get(ctx, key)
				if err != nil {
					continue
				}
				var svc ServiceRecord
				if json.Unmarshal(entry.Value(), &svc) == nil {
					fmt.Printf("  - %s: %s (port %d, %s)\n", svc.Service, svc.Health, svc.Port, svc.Protocol)
				}
			}
		}
	}

	return nil
}

func runAppMove(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	appName := args[0]
	fromNode, _ := cmd.Flags().GetString("from")
	toNode, _ := cmd.Flags().GetString("to")
	timeout, _ := cmd.Flags().GetDuration("timeout")

	if fromNode == "" {
		return fmt.Errorf("--from flag is required")
	}

	fmt.Printf("Moving app %s from %s", appName, fromNode)
	if toNode != "" {
		fmt.Printf(" to %s", toNode)
	} else {
		fmt.Printf(" (auto-selecting target)")
	}
	fmt.Printf("\nTimeout: %v\n", timeout)
	fmt.Println("\nNote: App migration requires connection to running platform.")
	fmt.Println("This operation should be performed via the go-cluster library API:")
	fmt.Println("")
	fmt.Println("  platform.MoveApp(ctx, cluster.MoveRequest{")
	fmt.Printf("      App:      %q,\n", appName)
	fmt.Printf("      FromNode: %q,\n", fromNode)
	if toNode != "" {
		fmt.Printf("      ToNode:   %q,\n", toNode)
	}
	fmt.Println("  })")

	return nil
}

func runAppRebalance(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	appName := args[0]
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	fmt.Printf("Rebalancing app %s\n", appName)
	if dryRun {
		fmt.Println("(dry-run mode - no changes will be made)")
	}
	fmt.Println("\nNote: App rebalancing requires connection to running platform.")
	fmt.Println("This operation should be performed via the go-cluster library API:")
	fmt.Println("")
	fmt.Printf("  platform.RebalanceApp(ctx, %q)\n", appName)

	return nil
}
