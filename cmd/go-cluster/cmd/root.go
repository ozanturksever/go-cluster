// Package cmd provides the CLI commands for go-cluster.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile   string
	natsURL   string
	nodeID    string
	platform  string
	verbose   bool
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "go-cluster",
	Short: "A clustering toolkit for building HA applications in Go",
	Long: `go-cluster is a NATS-native clustering toolkit that provides:
  - Leader election via NATS KV
  - Distributed KV and locks
  - SQLite3 WAL replication
  - Service discovery
  - Multi-zone deployments via NATS leaf nodes

Use go-cluster to manage cluster nodes, apps, and services.`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default $HOME/.go-cluster.yaml)")
	rootCmd.PersistentFlags().StringVarP(&natsURL, "nats", "n", "nats://localhost:4222", "NATS server URL")
	rootCmd.PersistentFlags().StringVar(&nodeID, "node", "", "Node ID (default: hostname)")
	rootCmd.PersistentFlags().StringVarP(&platform, "platform", "p", "", "Platform name")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")

	// Bind flags to viper
	viper.BindPFlag("nats_url", rootCmd.PersistentFlags().Lookup("nats"))
	viper.BindPFlag("node_id", rootCmd.PersistentFlags().Lookup("node"))
	viper.BindPFlag("platform", rootCmd.PersistentFlags().Lookup("platform"))
	viper.BindPFlag("verbose", rootCmd.PersistentFlags().Lookup("verbose"))

	// Environment variable bindings
	viper.BindEnv("nats_url", "NATS_URL")
	viper.BindEnv("node_id", "NODE_ID")
	viper.BindEnv("platform", "CLUSTER_ID", "PLATFORM")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Warning: could not find home directory:", err)
		} else {
			viper.AddConfigPath(home)
		}
		viper.AddConfigPath(".")
		viper.AddConfigPath("/etc/go-cluster")
		viper.SetConfigType("yaml")
		viper.SetConfigName(".go-cluster")
	}

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		if verbose {
			fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
		}
	}
}

// getNATSURL returns the NATS URL from config or flag.
func getNATSURL() string {
	if natsURL != "" {
		return natsURL
	}
	return viper.GetString("nats_url")
}

// getNodeID returns the node ID from config, flag, or hostname.
func getNodeID() string {
	if nodeID != "" {
		return nodeID
	}
	if id := viper.GetString("node_id"); id != "" {
		return id
	}
	hostname, _ := os.Hostname()
	return hostname
}

// getPlatform returns the platform name from config or flag.
func getPlatform() string {
	if platform != "" {
		return platform
	}
	return viper.GetString("platform")
}
