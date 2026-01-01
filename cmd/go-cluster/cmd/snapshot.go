package cmd

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/spf13/cobra"
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Manage database snapshots",
	Long:  `Commands for managing SQLite database snapshots.`,
}

var snapshotCreateCmd = &cobra.Command{
	Use:   "create <app-name>",
	Short: "Create a new snapshot",
	Args:  cobra.ExactArgs(1),
	RunE:  runSnapshotCreate,
}

var snapshotListCmd = &cobra.Command{
	Use:   "list <app-name>",
	Short: "List available snapshots",
	Args:  cobra.ExactArgs(1),
	RunE:  runSnapshotList,
}

var snapshotRestoreCmd = &cobra.Command{
	Use:   "restore <app-name> <snapshot-id>",
	Short: "Restore from a snapshot",
	Args:  cobra.ExactArgs(2),
	RunE:  runSnapshotRestore,
}

var snapshotDeleteCmd = &cobra.Command{
	Use:   "delete <app-name> <snapshot-id>",
	Short: "Delete a snapshot",
	Args:  cobra.ExactArgs(2),
	RunE:  runSnapshotDelete,
}

func init() {
	rootCmd.AddCommand(snapshotCmd)
	snapshotCmd.AddCommand(snapshotCreateCmd)
	snapshotCmd.AddCommand(snapshotListCmd)
	snapshotCmd.AddCommand(snapshotRestoreCmd)
	snapshotCmd.AddCommand(snapshotDeleteCmd)

	// Restore flags
	snapshotRestoreCmd.Flags().String("target", "", "Target path for restored database")
	snapshotRestoreCmd.Flags().Bool("force", false, "Force restore even if database exists")

	// List flags
	snapshotListCmd.Flags().Int("limit", 10, "Maximum number of snapshots to list")
}

// SnapshotInfo represents snapshot metadata
type SnapshotInfo struct {
	ID        string    `json:"id"`
	App       string    `json:"app"`
	Node      string    `json:"node"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
	Checksum  string    `json:"checksum,omitempty"`
}

func runSnapshotCreate(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	appName := args[0]

	fmt.Printf("Creating snapshot for app %s...\n", appName)
	fmt.Println("\nNote: Snapshot creation requires connection to running platform.")
	fmt.Println("This operation should be performed via the go-cluster library API:")
	fmt.Println("")
	fmt.Println("  app.Snapshots().Create(ctx)")
	fmt.Println("")
	fmt.Println("Or from within a running app:")
	fmt.Println("")
	fmt.Println("  snapshotID, err := app.Snapshots().Create(ctx)")

	return nil
}

func runSnapshotList(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	appName := args[0]
	limit, _ := cmd.Flags().GetInt("limit")

	nc, err := nats.Connect(getNATSURL())
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("failed to create JetStream context: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Try to access the snapshots object store
	bucketName := fmt.Sprintf("%s_%s_snapshots", platformName, appName)
	obj, err := js.ObjectStore(ctx, bucketName)
	if err != nil {
		fmt.Printf("No snapshots found for app %s\n", appName)
		fmt.Println("(Object store bucket not found)")
		return nil
	}

	// List objects in the bucket
	objects, err := obj.List(ctx)
	if err != nil {
		if err.Error() == "nats: no objects found" {
			fmt.Printf("No snapshots found for app %s\n", appName)
			return nil
		}
		return fmt.Errorf("failed to list snapshots: %w", err)
	}

	if len(objects) == 0 {
		fmt.Printf("No snapshots found for app %s\n", appName)
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSIZE\tCREATED\tCHECKSUM")

	count := 0
	for _, info := range objects {
		if count >= limit {
			break
		}

		// Parse metadata if available
		checksum := "-"
		if info.Digest != "" && len(info.Digest) > 12 {
			checksum = info.Digest[:12] + "..."
		} else if info.Digest != "" {
			checksum = info.Digest
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			info.Name,
			formatBytes(int64(info.Size)),
			info.ModTime.Format(time.RFC3339),
			checksum,
		)
		count++
	}

	return w.Flush()
}

func runSnapshotRestore(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	appName := args[0]
	snapshotID := args[1]
	targetPath, _ := cmd.Flags().GetString("target")
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Access the snapshots object store
	bucketName := fmt.Sprintf("%s_%s_snapshots", platformName, appName)
	obj, err := js.ObjectStore(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("snapshot bucket not found: %w", err)
	}

	// Get snapshot info
	info, err := obj.GetInfo(ctx, snapshotID)
	if err != nil {
		return fmt.Errorf("snapshot %q not found: %w", snapshotID, err)
	}

	fmt.Printf("Snapshot: %s\n", snapshotID)
	fmt.Printf("Size: %s\n", formatBytes(int64(info.Size)))
	fmt.Printf("Created: %s\n", info.ModTime.Format(time.RFC3339))

	if targetPath == "" {
		targetPath = fmt.Sprintf("./%s-restored.db", appName)
	}

	// Check if target exists
	if _, err := os.Stat(targetPath); err == nil && !force {
		return fmt.Errorf("target file %q already exists (use --force to overwrite)", targetPath)
	}

	fmt.Printf("\nRestoring to: %s\n", targetPath)

	// Download snapshot
	result, err := obj.Get(ctx, snapshotID)
	if err != nil {
		return fmt.Errorf("failed to download snapshot: %w", err)
	}
	defer result.Close()

	// Create target file
	f, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("failed to create target file: %w", err)
	}
	defer f.Close()

	// Copy data
	buf := make([]byte, 32*1024)
	var written int64
	for {
		n, err := result.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return fmt.Errorf("failed to write data: %w", werr)
			}
			written += int64(n)
		}
		if err != nil {
			break
		}
	}

	fmt.Printf("\n✓ Restored %s to %s\n", formatBytes(written), targetPath)
	return nil
}

func runSnapshotDelete(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	appName := args[0]
	snapshotID := args[1]

	nc, err := nats.Connect(getNATSURL())
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("failed to create JetStream context: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bucketName := fmt.Sprintf("%s_%s_snapshots", platformName, appName)
	obj, err := js.ObjectStore(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("snapshot bucket not found: %w", err)
	}

	if err := obj.Delete(ctx, snapshotID); err != nil {
		return fmt.Errorf("failed to delete snapshot: %w", err)
	}

	fmt.Printf("✓ Deleted snapshot %s\n", snapshotID)
	return nil
}

// formatBytes formats bytes to human readable string
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
