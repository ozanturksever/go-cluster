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

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show cluster status",
	RunE:  runStatus,
}

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check cluster health",
	RunE:  runHealth,
}

func init() {
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(healthCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
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

	fmt.Printf("Platform: %s\n", platformName)
	fmt.Printf("NATS: %s\n", getNATSURL())
	fmt.Println()

	// Get members
	membersBucket := fmt.Sprintf("%s_members", platformName)
	membersKV, err := js.KeyValue(ctx, membersBucket)
	if err != nil {
		fmt.Printf("Members: (bucket not found)\n")
	} else {
		keys, err := membersKV.Keys(ctx)
		if err != nil {
			fmt.Printf("Members: 0\n")
		} else {
			healthy := 0
			for _, key := range keys {
				entry, err := membersKV.Get(ctx, key)
				if err != nil {
					continue
				}
				var member MemberRecord
				if json.Unmarshal(entry.Value(), &member) == nil {
					if member.IsHealthy && time.Since(member.LastSeen) < 30*time.Second {
						healthy++
					}
				}
			}
			fmt.Printf("Nodes: %d total, %d healthy\n", len(keys), healthy)
		}
	}

	// Get services
	servicesBucket := fmt.Sprintf("%s_services", platformName)
	servicesKV, err := js.KeyValue(ctx, servicesBucket)
	if err != nil {
		fmt.Printf("Services: (bucket not found)\n")
	} else {
		keys, err := servicesKV.Keys(ctx)
		if err != nil {
			fmt.Printf("Services: 0\n")
		} else {
			fmt.Printf("Services: %d registered\n", len(keys))
		}
	}

	return nil
}

func runHealth(cmd *cobra.Command, args []string) error {
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

	// Check NATS connection
	fmt.Println("Checking cluster health...")
	fmt.Println()

	fmt.Printf("✓ NATS connection: OK\n")

	// Check JetStream
	_, err = js.AccountInfo(ctx)
	if err != nil {
		fmt.Printf("✗ JetStream: %v\n", err)
	} else {
		fmt.Printf("✓ JetStream: OK\n")
	}

	// Check members bucket
	membersBucket := fmt.Sprintf("%s_members", platformName)
	membersKV, err := js.KeyValue(ctx, membersBucket)
	if err != nil {
		fmt.Printf("✗ Members bucket: not found\n")
	} else {
		keys, _ := membersKV.Keys(ctx)
		fmt.Printf("✓ Members bucket: %d nodes\n", len(keys))

		// Show node health
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "\n  NODE\tSTATUS\tLAST SEEN")
		for _, key := range keys {
			entry, err := membersKV.Get(ctx, key)
			if err != nil {
				continue
			}
			var member MemberRecord
			if json.Unmarshal(entry.Value(), &member) == nil {
				status := "✓ healthy"
				if !member.IsHealthy {
					status = "✗ unhealthy"
				}
				if time.Since(member.LastSeen) > 30*time.Second {
					status = "? unknown"
				}
				fmt.Fprintf(w, "  %s\t%s\t%s\n", member.NodeID, status, time.Since(member.LastSeen).Round(time.Second))
			}
		}
		w.Flush()
	}

	return nil
}
