package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type ProcessConfig struct {
	Name            string        `yaml:"name"`
	Command         string        `yaml:"command"`
	Args            []string      `yaml:"args"`
	StartTimeout    time.Duration `yaml:"start_timeout"`
	RestartDelay    time.Duration `yaml:"restart_delay"`
	MaxRestarts     int           `yaml:"max_restarts"`
	WorkingDir      string        `yaml:"working_dir"`
	Environment     []string      `yaml:"environment"`
	BackoffEnabled  bool          `yaml:"backoff_enabled"`
	BackoffFactor   float64       `yaml:"backoff_factor"`
	BackoffMaxDelay time.Duration `yaml:"backoff_max_delay"`
	StopTimeout     time.Duration `yaml:"stop_timeout"` // How long to wait for graceful shutdown
	NumProcs        int           `yaml:"numprocs"`     // Number of process instances to run
	ExitCodes       []int         `yaml:"exit_codes"`   // Normal exit codes that don't trigger restart
}

type Config struct {
	LogFormat     string          `yaml:"log_format"`
	MetricsAddr   string          `yaml:"metrics_addr"`
	WebAddr       string          `yaml:"web_addr"`
	Processes     []ProcessConfig `yaml:"processes"`
	ShutdownDelay time.Duration   `yaml:"shutdown_delay"` // How long to wait for processes to shutdown
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Set defaults
	if config.LogFormat == "" {
		config.LogFormat = "console"
	}
	if config.MetricsAddr == "" {
		config.MetricsAddr = ":9090"
	}
	if config.ShutdownDelay == 0 {
		config.ShutdownDelay = 10 * time.Second
	}

	// Set process defaults
	for i := range config.Processes {
		if config.Processes[i].StartTimeout == 0 {
			config.Processes[i].StartTimeout = 30 * time.Second
		}
		if config.Processes[i].StopTimeout == 0 {
			config.Processes[i].StopTimeout = 10 * time.Second
		}
		if config.Processes[i].RestartDelay == 0 {
			config.Processes[i].RestartDelay = 5 * time.Second
		}
		if config.Processes[i].NumProcs == 0 {
			config.Processes[i].NumProcs = 1
		}
		if config.Processes[i].MaxRestarts == 0 {
			config.Processes[i].MaxRestarts = 5
		}
		if config.Processes[i].BackoffFactor == 0 {
			config.Processes[i].BackoffFactor = 1.5
		}
		// Default to empty slice for exit codes (all codes will trigger restart)
		if config.Processes[i].ExitCodes == nil {
			config.Processes[i].ExitCodes = []int{}
		}
	}

	return &config, nil
}
