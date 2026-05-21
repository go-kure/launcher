package shared

import (
	"fmt"
	"os"

	"github.com/spf13/viper"

	"github.com/go-kure/launcher/pkg/cmd/shared/options"
)

// InitConfig initializes Viper configuration for a CLI tool
func InitConfig(appName string, globalOpts *options.GlobalOptions) {
	if globalOpts.ConfigFile != "" {
		viper.SetConfigFile(globalOpts.ConfigFile)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		viper.AddConfigPath(home)
		viper.AddConfigPath(".")
		viper.SetConfigName(fmt.Sprintf(".%s", appName))
		viper.SetConfigType("yaml")
	}

	viper.SetEnvPrefix(appName)
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil && globalOpts.Verbose {
		fmt.Fprintf(os.Stderr, "Using config file: %s\n", viper.ConfigFileUsed())
	}
}
