package kurel

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewKurelCommand(t *testing.T) {
	cmd := NewKurelCommand()

	if cmd == nil {
		t.Fatal("expected non-nil command")
	}

	if cmd.Use != "kurel" {
		t.Errorf("expected command name 'kurel', got %s", cmd.Use)
	}

	if cmd.Short == "" {
		t.Error("expected non-empty short description")
	}

	if cmd.Long == "" {
		t.Error("expected non-empty long description")
	}

	if !cmd.SilenceUsage {
		t.Error("expected SilenceUsage to be true")
	}

	if !cmd.SilenceErrors {
		t.Error("expected SilenceErrors to be true")
	}

	if cmd.PersistentPreRunE == nil {
		t.Error("expected PersistentPreRunE to be set")
	}
}

func TestKurelCommandSubcommands(t *testing.T) {
	cmd := NewKurelCommand()

	expectedSubcommands := []string{"build", "config", "completion", "version"}

	commandMap := make(map[string]bool)
	for _, subCmd := range cmd.Commands() {
		commandMap[extractCommandName(subCmd.Use)] = true
	}

	for _, expectedCmd := range expectedSubcommands {
		if !commandMap[expectedCmd] {
			t.Errorf("expected subcommand %s not found", expectedCmd)
		}
	}
}

func TestKurelCommandFlags(t *testing.T) {
	cmd := NewKurelCommand()

	expectedFlags := []string{"config", "verbose", "debug", "output", "dry-run", "namespace"}

	for _, flagName := range expectedFlags {
		flag := cmd.PersistentFlags().Lookup(flagName)
		if flag == nil {
			t.Errorf("expected persistent flag %s not found", flagName)
		}
	}
}

func TestKurelCommandHelp(t *testing.T) {
	cmd := NewKurelCommand()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("help command failed: %v", err)
	}

	output := buf.String()
	for _, content := range []string{"kurel", "Usage:", "Available Commands:", "Flags:"} {
		if !strings.Contains(output, content) {
			t.Errorf("expected help output to contain %q", content)
		}
	}
}

func TestKurelCommandPersistentPreRun(t *testing.T) {
	cmd := NewKurelCommand()
	cmd.SetArgs([]string{"--output=json", "--verbose"})
	if err := cmd.PersistentPreRunE(cmd, []string{}); err != nil {
		t.Errorf("persistent pre-run failed: %v", err)
	}
}

func TestKurelCommandVersion(t *testing.T) {
	cmd := NewKurelCommand()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("version command failed: %v", err)
	}
}

func TestKurelCommandCompletion(t *testing.T) {
	cmd := NewKurelCommand()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	cmd.SetArgs([]string{"completion", "bash"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("completion command failed: %v", err)
	}
}

func TestKurelCommandInvalidFlags(t *testing.T) {
	cmd := NewKurelCommand()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	cmd.SetArgs([]string{"--output=invalid-format", "version"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for invalid output format")
	}
}

func TestKurelCommandFlagValidation(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantError bool
	}{
		{"valid yaml output", []string{"--output=yaml", "version"}, false},
		{"valid json output", []string{"--output=json", "version"}, false},
		{"valid table output", []string{"--output=table", "version"}, false},
		{"invalid output format", []string{"--output=invalid", "version"}, true},
		{"valid verbose flag", []string{"--verbose", "version"}, false},
		{"valid debug flag", []string{"--debug", "version"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewKurelCommand()

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)

			cmd.SetArgs(tt.args)
			err := cmd.Execute()

			if tt.wantError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestKurelCommandExecuteError(t *testing.T) {
	cmd := NewKurelCommand()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	cmd.SetArgs([]string{"nonexistent-command"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for nonexistent command")
	}
}

func TestNewConfigCommand(t *testing.T) {
	cmd := NewKurelCommand()

	commandMap := make(map[string]bool)
	for _, subCmd := range cmd.Commands() {
		commandMap[extractCommandName(subCmd.Use)] = true
	}

	if !commandMap["config"] {
		t.Fatal("config subcommand not found")
	}
}

func TestConfigCommandSubcommands(t *testing.T) {
	cmd := NewKurelCommand()

	var configSubs []string
	for _, sub := range cmd.Commands() {
		if extractCommandName(sub.Use) == "config" {
			for _, s := range sub.Commands() {
				configSubs = append(configSubs, extractCommandName(s.Use))
			}
			break
		}
	}
	if len(configSubs) == 0 {
		t.Fatal("config command not found or has no subcommands")
	}

	commandMap := make(map[string]bool)
	for _, name := range configSubs {
		commandMap[name] = true
	}

	for _, expected := range []string{"view", "init"} {
		if !commandMap[expected] {
			t.Errorf("expected config subcommand %q not found", expected)
		}
	}
}

func TestConfigViewCommand(t *testing.T) {
	cmd := NewKurelCommand()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	cmd.SetArgs([]string{"config", "view"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("config view failed: %v", err)
	}
}

func TestConfigInitCommand(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, ".kurel", "config.yaml")

	cmd := NewKurelCommand()
	cmd.SetArgs([]string{"--config=" + configPath, "config", "init"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Errorf("config init failed: %v", err)
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("expected config file to be created")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read created config: %v", err)
	}

	for _, expected := range []string{"verbose: false", "debug: false", "strict: false"} {
		if !strings.Contains(string(data), expected) {
			t.Errorf("expected config file to contain %q", expected)
		}
	}
}

func TestConfigInitCommandDefaultPath(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(origDir); err != nil {
			t.Errorf("failed to restore working directory: %v", err)
		}
	}()

	cmd := NewKurelCommand()
	cmd.SetArgs([]string{"config", "init"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Errorf("config init with default path failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, ".kurel", "config.yaml")); os.IsNotExist(err) {
		t.Error("expected default config file to be created at .kurel/config.yaml")
	}
}

func TestConfigCommandHelp(t *testing.T) {
	cmd := NewKurelCommand()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	cmd.SetArgs([]string{"config", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("config help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "config") {
		t.Error("expected config help to contain 'config'")
	}
}

func TestKurelCompletionShellVariants(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			cmd := NewKurelCommand()

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)

			cmd.SetArgs([]string{"completion", shell})
			if err := cmd.Execute(); err != nil {
				t.Errorf("completion %s failed: %v", shell, err)
			}
		})
	}
}

func extractCommandName(use string) string {
	for i, char := range use {
		if char == ' ' || char == '[' || char == '<' {
			return use[:i]
		}
	}
	return use
}
