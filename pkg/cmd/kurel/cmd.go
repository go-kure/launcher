package kurel

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/go-kure/kure/pkg/cmd/shared"
	"github.com/go-kure/kure/pkg/cmd/shared/options"
	"github.com/go-kure/kure/pkg/errors"
)

// NewKurelCommand creates the root command for kurel CLI
func NewKurelCommand() *cobra.Command {
	globalOpts := options.NewGlobalOptions()

	cmd := &cobra.Command{
		Use:   "kurel",
		Short: "Kurel - OAM-native Kubernetes package manager",
		Long: `Kurel is an OAM-inspired package manager for Kubernetes.

Packages are described using launcher Application documents (app.yaml) and
a parameter schema (kurel.yaml). Build-time parameter substitution produces
static, GitOps-ready Kubernetes manifests.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return globalOpts.Complete()
		},
	}

	globalOpts.AddFlags(cmd.PersistentFlags())
	shared.InitConfig("kurel", globalOpts)

	cmd.AddCommand(
		newConfigCommand(globalOpts),
		shared.NewCompletionCommand(),
		shared.NewVersionCommand("kurel"),
	)

	return cmd
}

// Execute runs the root command
func Execute() {
	cmd := NewKurelCommand()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newConfigCommand(globalOpts *options.GlobalOptions) *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage kurel configuration",
		Long:  "View and modify kurel configuration settings",
	}

	configCmd.AddCommand(&cobra.Command{
		Use:   "view",
		Short: "View current configuration",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Configuration:")
			fmt.Printf("  Verbose: %v\n", globalOpts.Verbose)
			fmt.Printf("  Debug: %v\n", globalOpts.Debug)
			fmt.Printf("  Strict: %v\n", globalOpts.Strict)
			fmt.Printf("  Config File: %s\n", globalOpts.ConfigFile)
		},
	})

	configCmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Initialize configuration file",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath := filepath.Join(".kurel", "config.yaml")
			if globalOpts.ConfigFile != "" {
				configPath = globalOpts.ConfigFile
			}

			dir := filepath.Dir(configPath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return errors.Wrap(err, "failed to create config directory")
			}

			defaultConfig := `# Kurel Configuration
verbose: false
debug: false
strict: false
`
			if err := os.WriteFile(configPath, []byte(defaultConfig), 0644); err != nil {
				return errors.Wrap(err, "failed to write config file")
			}

			fmt.Printf("Configuration initialized at %s\n", configPath)
			return nil
		},
	})

	return configCmd
}
