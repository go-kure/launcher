package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/cmd/kurel"
)

func TestMain_Integration(t *testing.T) {
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	os.Args = []string{"kurel", "--help"}

	cmd := kurel.NewKurelCommand()
	if cmd == nil {
		t.Fatal("NewKurelCommand returned nil")
	}

	if cmd.Use != "kurel" {
		t.Errorf("Command name = %s, want kurel", cmd.Use)
	}

	if cmd.Short == "" {
		t.Error("Command should have a short description")
	}

	if cmd.Long == "" {
		t.Error("Command should have a long description")
	}
}

func TestMain_HelpCommand(t *testing.T) {
	cmd := kurel.NewKurelCommand()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("Help command failed: %v", err)
	}

	output := buf.String()
	if output == "" {
		t.Error("Help command produced no output")
	}

	for _, content := range []string{"kurel", "Usage:", "Available Commands:", "Flags:"} {
		if !strings.Contains(output, content) {
			t.Errorf("Help output missing expected content: %s", content)
		}
	}
}

func TestMain_VersionCommand(t *testing.T) {
	cmd := kurel.NewKurelCommand()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("Version command failed: %v", err)
	}
}

func TestMain_InvalidCommand(t *testing.T) {
	cmd := kurel.NewKurelCommand()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	cmd.SetArgs([]string{"invalid-command"})
	if err := cmd.Execute(); err == nil {
		t.Error("Expected error for invalid command, got nil")
	}
}

func TestMain_CommandStructure(t *testing.T) {
	cmd := kurel.NewKurelCommand()

	commandNames := make(map[string]bool)
	for _, subCmd := range cmd.Commands() {
		parts := strings.Fields(subCmd.Use)
		if len(parts) > 0 {
			commandNames[parts[0]] = true
		}
	}

	for _, expectedCmd := range []string{"config", "completion", "version"} {
		if !commandNames[expectedCmd] {
			t.Errorf("Expected subcommand %s not found", expectedCmd)
		}
	}
}

func TestMain_PersistentFlags(t *testing.T) {
	cmd := kurel.NewKurelCommand()

	for _, flagName := range []string{"config", "verbose", "debug", "output", "dry-run", "namespace"} {
		flag := cmd.PersistentFlags().Lookup(flagName)
		if flag == nil {
			t.Errorf("Expected persistent flag %s not found", flagName)
		}
	}
}

func TestMain_CommandDefaults(t *testing.T) {
	cmd := kurel.NewKurelCommand()

	if !cmd.SilenceUsage {
		t.Error("Expected SilenceUsage to be true")
	}

	if !cmd.SilenceErrors {
		t.Error("Expected SilenceErrors to be true")
	}

	if cmd.PersistentPreRunE == nil {
		t.Error("Expected PersistentPreRunE to be set")
	}
}

func TestMain_FlagValidation(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantError bool
	}{
		{"valid output yaml", []string{"--output=yaml", "version"}, false},
		{"valid output json", []string{"--output=json", "version"}, false},
		{"invalid output format", []string{"--output=invalid", "version"}, true},
		{"verbose flag", []string{"--verbose", "version"}, false},
		{"debug flag", []string{"--debug", "version"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := kurel.NewKurelCommand()

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)

			cmd.SetArgs(tt.args)
			err := cmd.Execute()

			if tt.wantError && err == nil {
				t.Error("Expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestMain_CompletionCommand(t *testing.T) {
	cmd := kurel.NewKurelCommand()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	cmd.SetArgs([]string{"completion", "bash"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("Completion command failed: %v", err)
	}
}

func TestMain_ExecuteFunction(t *testing.T) {
	cmd := kurel.NewKurelCommand()
	if cmd == nil {
		t.Fatal("kurel.NewKurelCommand() returned nil")
	}

	if cmd.Use != "kurel" {
		t.Errorf("Expected command use 'kurel', got %s", cmd.Use)
	}

	cmd.SetArgs([]string{"--help"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Errorf("Command execution failed: %v", err)
	}
}

func TestMain_PersistentPreRun(t *testing.T) {
	cmd := kurel.NewKurelCommand()
	if err := cmd.PersistentPreRunE(cmd, []string{}); err != nil {
		t.Errorf("PersistentPreRunE failed: %v", err)
	}
}

func TestMain_CommandUsage(t *testing.T) {
	cmd := kurel.NewKurelCommand()

	usage := cmd.UsageString()
	if usage == "" {
		t.Error("Command usage string is empty")
	}

	if !strings.Contains(usage, "kurel") {
		t.Error("Usage string should contain 'kurel'")
	}
}

func TestMain_ConfigCommand(t *testing.T) {
	cmd := kurel.NewKurelCommand()

	found := false
	for _, subCmd := range cmd.Commands() {
		if strings.HasPrefix(subCmd.Use, "config") {
			found = true
			if subCmd.Short == "" {
				t.Error("Config command should have a short description")
			}
			if subCmd.Long == "" {
				t.Error("Config command should have a long description")
			}
			break
		}
	}

	if !found {
		t.Error("Config command not found")
	}
}

func TestMain_CommandAliases(t *testing.T) {
	cmd := kurel.NewKurelCommand()

	for _, subCmd := range cmd.Commands() {
		for _, alias := range subCmd.Aliases {
			if alias == "" {
				t.Errorf("Command %s has empty alias", subCmd.Use)
			}
		}
	}
}
