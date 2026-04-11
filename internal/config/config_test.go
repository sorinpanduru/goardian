package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	// Create a temporary directory for test files
	tmpDir := t.TempDir()

	// Create a test config file
	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `processes:
  - name: "randomsleeper 1"
    command: "/usr/local/bin/randomsleeper"
    args: ["-processid", "1"]
    working_dir: "."
    environment:
      - "TEST_VAR=test_value"
    numprocs: 1
    start_timeout: "1s"
    stop_timeout: "3s"
    restart_delay: "3s"
    max_restarts: 10
    backoff_enabled: true
    backoff_factor: 1.5
    backoff_max_delay: "1m"
    exit_codes: [0]`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Load the config
	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)
	assert.NotNil(t, cfg)

	// Verify the config contents
	assert.Len(t, cfg.Processes, 1)
	proc := cfg.Processes[0]
	assert.Equal(t, "randomsleeper 1", proc.Name)
	assert.Equal(t, "/usr/local/bin/randomsleeper", proc.Command)
	assert.Equal(t, []string{"-processid", "1"}, proc.Args)
	assert.Equal(t, 1, proc.NumProcs)
	assert.Equal(t, "TEST_VAR=test_value", proc.Environment[0])
}

func TestLoadConfig_InvalidFile(t *testing.T) {
	// Try to load a non-existent file
	_, err := LoadConfig("/non/existent/file.yaml")
	assert.Error(t, err)
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	// Create a temporary directory for test files
	tmpDir := t.TempDir()

	// Create an invalid YAML file
	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `
processes:
  - name: test-process
    command: /bin/sleep
    args: ["5"
    numprocs: 1
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Try to load the invalid config
	_, err = LoadConfig(configPath)
	assert.Error(t, err)
}

func TestLoadConfig_DuplicateProcessNames(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `processes:
  - name: "myproc"
    command: "/bin/sleep"
    args: ["5"]
  - name: "myproc"
    command: "/bin/echo"
    args: ["hello"]
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	_, err = LoadConfig(configPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate process name")
}

func TestLoadConfig_MissingCommand(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `processes:
  - name: "nocommand"
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	_, err = LoadConfig(configPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "command is required")
}

func TestLoadConfig_InvalidLogFormat(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `log_format: "xml"
processes:
  - name: "proc1"
    command: "/bin/sleep"
    args: ["5"]
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	_, err = LoadConfig(configPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid log_format")
}
