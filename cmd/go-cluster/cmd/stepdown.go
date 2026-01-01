package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/spf13/cobra"
)

var stepdownCmd = &cobra.Command{
	Use:   "stepdown <app-name>",
	Short: "Gracefully step down as leader",
	Long: `Request the current leader to step down and trigger a new election.

This command sends a step-down request to the current leader of the specified app.
The leader will gracefully release leadership, allowing another node to take over.

Useful for:
- Planned maintenance
- Rolling upgrades
- Testing failover behavior`,
	Args: cobra.ExactArgs(1),
	RunE: runStepdown,
}

func init() {
	rootCmd.AddCommand(stepdownCmd)

	stepdownCmd.Flags().Duration("timeout", 30*time.Second, "Timeout for step-down operation")
	stepdownCmd.Flags().Bool("force", false, "Force step-down even if no standby available")
}

func runStepdown(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	appName := args[0]
	timeout, _ := cmd.Flags().GetDuration("timeout")
	force, _ := cmd.Flags().GetBool("force")

	nc, err := nats.Connect(getNATSURL())
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("failed to create JetStream context: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Get current leader
	electionBucket := fmt.Sprintf("%s_%s_election", platformName, appName)
	kv, err := js.KeyValue(ctx, electionBucket)
	if err != nil {
		return fmt.Errorf("election bucket not found for app %q", appName)
	}

	entry, err := kv.Get(ctx, "leader")
	if err != nil {
		return fmt.Errorf("no leader found for app %q", appName)
	}

	var election ElectionRecord
	if err := json.Unmarshal(entry.Value(), &election); err != nil {
		return fmt.Errorf("failed to parse election data: %w", err)
	}

	if election.Leader == "" {
		fmt.Printf("No current leader for app %s\n", appName)
		return nil
	}

	fmt.Printf("Current leader: %s (epoch %d)\n", election.Leader, election.Epoch)
	fmt.Printf("Requesting step-down...\n")

	// Send step-down request via NATS
	subject := fmt.Sprintf("%s.%s.election.stepdown", platformName, appName)
	reqData := []byte(fmt.Sprintf(`{"force":%v}`, force))

	resp, err := nc.Request(subject, reqData, timeout)
	if err != nil {
		if err == nats.ErrNoResponders {
			fmt.Println("\nNo leader responded to step-down request.")
			fmt.Println("The leader may not be running or may not support remote step-down.")
			fmt.Println("\nAlternative: Use the go-cluster library API directly:")
			fmt.Println("  app.Election().StepDown(ctx)")
			return nil
		}
		return fmt.Errorf("step-down request failed: %w", err)
	}

	fmt.Printf("\n%s\n", string(resp.Data))

	// Wait and check for new leader
	fmt.Println("\nWaiting for new leader...")
	time.Sleep(2 * time.Second)

	entry, err = kv.Get(ctx, "leader")
	if err == nil {
		if err := json.Unmarshal(entry.Value(), &election); err == nil {
			fmt.Printf("New leader: %s (epoch %d)\n", election.Leader, election.Epoch)
		}
	}

	return nil
}
