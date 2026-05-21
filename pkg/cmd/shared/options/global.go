package options

import (
	"os"
	"slices"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/go-kure/launcher/pkg/errors"
)

// GlobalOptions contains global flags and configuration
type GlobalOptions struct {
	ConfigFile string
	Verbose    bool
	Debug      bool
	Strict     bool

	Output     string
	OutputFile string
	NoHeaders  bool
	ShowLabels bool
	Wide       bool

	DryRun    bool
	Namespace string
}

// NewGlobalOptions creates a new GlobalOptions with defaults
func NewGlobalOptions() *GlobalOptions {
	return &GlobalOptions{
		Output:  "yaml",
		Verbose: false,
		Debug:   false,
		DryRun:  false,
	}
}

// AddFlags adds global flags to the provided FlagSet
func (o *GlobalOptions) AddFlags(flags *pflag.FlagSet) {
	flags.StringVarP(&o.ConfigFile, "config", "c", o.ConfigFile, "config file (default is $HOME/.kurel.yaml)")
	flags.BoolVarP(&o.Verbose, "verbose", "v", o.Verbose, "verbose output")
	flags.BoolVar(&o.Debug, "debug", o.Debug, "debug output")
	flags.BoolVar(&o.Strict, "strict", o.Strict, "treat warnings as errors")

	flags.StringVarP(&o.Output, "output", "o", o.Output, "output format (yaml|json|table|wide|name)")
	flags.StringVarP(&o.OutputFile, "output-file", "f", o.OutputFile, "write output to file instead of stdout")
	flags.BoolVar(&o.NoHeaders, "no-headers", o.NoHeaders, "don't print headers (for table output)")
	flags.BoolVar(&o.ShowLabels, "show-labels", o.ShowLabels, "show resource labels in table output")
	flags.BoolVar(&o.Wide, "wide", o.Wide, "use wide output format")

	flags.BoolVar(&o.DryRun, "dry-run", o.DryRun, "print generated resources without writing to files")
	flags.StringVarP(&o.Namespace, "namespace", "n", o.Namespace, "target namespace for operations")
}

// Complete completes the global options by reading from configuration
func (o *GlobalOptions) Complete() error {
	if viper.IsSet("verbose") {
		o.Verbose = viper.GetBool("verbose")
	}
	if viper.IsSet("debug") {
		o.Debug = viper.GetBool("debug")
	}
	if viper.IsSet("output") {
		o.Output = viper.GetString("output")
	}
	if viper.IsSet("namespace") {
		o.Namespace = viper.GetString("namespace")
	}

	if o.Debug {
		_ = os.Setenv("KUREL_DEBUG", "1")
		o.Verbose = true
	}

	return o.Validate()
}

// Validate validates the global options
func (o *GlobalOptions) Validate() error {
	validOutputs := []string{"yaml", "json", "table", "wide", "name"}
	if !slices.Contains(validOutputs, o.Output) {
		return errors.Errorf("invalid output format %q: must be one of %v", o.Output, validOutputs)
	}
	if o.Output == "wide" {
		o.Wide = true
		o.Output = "table"
	}
	return nil
}
