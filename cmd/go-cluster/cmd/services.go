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

var servicesCmd = &cobra.Command{
	Use:   "services",
	Short: "Manage services",
	Long:  `Commands for discovering and managing services.`,
}

var servicesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered services",
	RunE:  runServicesList,
}

var servicesShowCmd = &cobra.Command{
	Use:   "show <app> <service>",
	Short: "Show service details",
	Args:  cobra.ExactArgs(2),
	RunE:  runServicesShow,
}

var servicesExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export services in various formats",
	RunE:  runServicesExport,
}

func init() {
	rootCmd.AddCommand(servicesCmd)
	servicesCmd.AddCommand(servicesListCmd)
	servicesCmd.AddCommand(servicesShowCmd)
	servicesCmd.AddCommand(servicesExportCmd)

	// Export flags
	servicesExportCmd.Flags().String("format", "json", "Export format: json, prometheus, consul")
}

// ServiceRecord represents a service instance (matches service_discovery.go)
type ServiceRecord struct {
	App       string            `json:"app"`
	Service   string            `json:"service"`
	NodeID    string            `json:"node_id"`
	Port      int               `json:"port"`
	Protocol  string            `json:"protocol"`
	Path      string            `json:"path,omitempty"`
	Tags      []string          `json:"tags,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Health    string            `json:"health"`
	UpdatedAt time.Time         `json:"updated_at"`
}

func runServicesList(cmd *cobra.Command, args []string) error {
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

	bucketName := fmt.Sprintf("%s_services", platformName)
	kv, err := js.KeyValue(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("failed to access services bucket: %w", err)
	}

	keys, err := kv.Keys(ctx)
	if err != nil {
		if err.Error() == "nats: no keys found" {
			fmt.Println("No services found")
			return nil
		}
		return fmt.Errorf("failed to list services: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "APP\tSERVICE\tNODE\tPORT\tPROTOCOL\tHEALTH\tTAGS")

	for _, key := range keys {
		entry, err := kv.Get(ctx, key)
		if err != nil {
			continue
		}

		var svc ServiceRecord
		if err := json.Unmarshal(entry.Value(), &svc); err != nil {
			continue
		}

		tags := "-"
		if len(svc.Tags) > 0 {
			tags = strings.Join(svc.Tags, ",")
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			svc.App,
			svc.Service,
			svc.NodeID,
			svc.Port,
			svc.Protocol,
			svc.Health,
			tags,
		)
	}

	return w.Flush()
}

func runServicesShow(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	appName := args[0]
	serviceName := args[1]

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

	bucketName := fmt.Sprintf("%s_services", platformName)
	kv, err := js.KeyValue(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("failed to access services bucket: %w", err)
	}

	// Find matching services
	keys, err := kv.Keys(ctx)
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	prefix := fmt.Sprintf("%s.%s.", appName, serviceName)
	found := false

	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		entry, err := kv.Get(ctx, key)
		if err != nil {
			continue
		}

		var svc ServiceRecord
		if err := json.Unmarshal(entry.Value(), &svc); err != nil {
			continue
		}

		if !found {
			fmt.Printf("Service: %s/%s\n\n", appName, serviceName)
			fmt.Println("Instances:")
		}
		found = true

		fmt.Printf("\n  Node: %s\n", svc.NodeID)
		fmt.Printf("  Port: %d\n", svc.Port)
		fmt.Printf("  Protocol: %s\n", svc.Protocol)
		if svc.Path != "" {
			fmt.Printf("  Path: %s\n", svc.Path)
		}
		fmt.Printf("  Health: %s\n", svc.Health)
		fmt.Printf("  Updated: %s\n", svc.UpdatedAt.Format(time.RFC3339))
		if len(svc.Tags) > 0 {
			fmt.Printf("  Tags: %s\n", strings.Join(svc.Tags, ", "))
		}
		if len(svc.Metadata) > 0 {
			fmt.Printf("  Metadata:\n")
			for k, v := range svc.Metadata {
				fmt.Printf("    %s: %s\n", k, v)
			}
		}
	}

	if !found {
		return fmt.Errorf("service %s/%s not found", appName, serviceName)
	}

	return nil
}

func runServicesExport(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required")
	}

	format, _ := cmd.Flags().GetString("format")

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

	bucketName := fmt.Sprintf("%s_services", platformName)
	kv, err := js.KeyValue(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("failed to access services bucket: %w", err)
	}

	keys, err := kv.Keys(ctx)
	if err != nil {
		if err.Error() == "nats: no keys found" {
			fmt.Println("[]")
			return nil
		}
		return fmt.Errorf("failed to list services: %w", err)
	}

	var services []ServiceRecord
	for _, key := range keys {
		entry, err := kv.Get(ctx, key)
		if err != nil {
			continue
		}

		var svc ServiceRecord
		if err := json.Unmarshal(entry.Value(), &svc); err != nil {
			continue
		}
		services = append(services, svc)
	}

	switch format {
	case "json":
		data, _ := json.MarshalIndent(services, "", "  ")
		fmt.Println(string(data))

	case "prometheus":
		// Export as Prometheus static config
		type Target struct {
			Targets []string          `json:"targets"`
			Labels  map[string]string `json:"labels"`
		}
		var targets []Target
		for _, svc := range services {
			if svc.Health != "passing" {
				continue
			}
			t := Target{
				Targets: []string{fmt.Sprintf("%s:%d", svc.NodeID, svc.Port)},
				Labels: map[string]string{
					"app":     svc.App,
					"service": svc.Service,
					"node":    svc.NodeID,
				},
			}
			targets = append(targets, t)
		}
		data, _ := json.MarshalIndent(targets, "", "  ")
		fmt.Println(string(data))

	case "consul":
		// Export as Consul-compatible format
		type ConsulService struct {
			ID      string            `json:"ID"`
			Name    string            `json:"Name"`
			Tags    []string          `json:"Tags"`
			Address string            `json:"Address"`
			Port    int               `json:"Port"`
			Meta    map[string]string `json:"Meta"`
		}
		var consulServices []ConsulService
		for _, svc := range services {
			cs := ConsulService{
				ID:      fmt.Sprintf("%s-%s-%s", svc.App, svc.Service, svc.NodeID),
				Name:    fmt.Sprintf("%s-%s", svc.App, svc.Service),
				Tags:    svc.Tags,
				Address: svc.NodeID,
				Port:    svc.Port,
				Meta:    svc.Metadata,
			}
			consulServices = append(consulServices, cs)
		}
		data, _ := json.MarshalIndent(consulServices, "", "  ")
		fmt.Println(string(data))

	default:
		return fmt.Errorf("unknown format: %s (use json, prometheus, or consul)", format)
	}

	return nil
}
