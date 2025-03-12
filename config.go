package main

import (
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
}

type Config struct {
	Processes []ProcessConfig `yaml:"processes"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}
