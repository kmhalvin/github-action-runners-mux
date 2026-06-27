package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type RunnerConfig struct {
	Name   string `yaml:"name"`
	URL    string `yaml:"url"`
	Token  string `yaml:"token"`
	Dir    string `yaml:"dir"`
	Labels string `yaml:"labels,omitempty"`
	Group  string `yaml:"group,omitempty"`
}

type Config struct {
	Runners []RunnerConfig `yaml:"runners"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if len(cfg.Runners) == 0 {
		return nil, fmt.Errorf("no runners configured")
	}

	for _, r := range cfg.Runners {
		if r.Name == "" || r.URL == "" || r.Dir == "" {
			return nil, fmt.Errorf("runner configuration is missing required fields (name, url, dir)")
		}
		// Token can be empty if it's already registered, but let's warn or handle later if it's not registered
	}

	return &cfg, nil
}
