// Package config handles configuration loading from YAML files with environment variable overrides.
package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration for matfmonitor
type Config struct {
	// Federation metadata settings
	MetadataURL string `yaml:"metadataURL"`
	JWKSPath    string `yaml:"jwksPath"`
	CachePath   string `yaml:"cachePath"`

	// Database settings
	DatabasePath string `yaml:"databasePath"`

	// Web server settings
	ListenAddress string `yaml:"listenAddress"`

	// Health check limits
	MaxParallelChecks int           `yaml:"maxParallelChecks"`
	ChecksPerMinute   int           `yaml:"checksPerMinute"`
	MinCheckInterval  time.Duration `yaml:"minCheckInterval"`

	// TLS settings
	TLSTimeout time.Duration `yaml:"tlsTimeout"`
}

// DefaultConfig returns a Config with default values
func DefaultConfig() *Config {
	return &Config{
		DatabasePath:      "./matfmonitor.db",
		ListenAddress:     ":8080",
		MaxParallelChecks: 5,
		ChecksPerMinute:   20,
		MinCheckInterval:  5 * time.Hour,
		TLSTimeout:        10 * time.Second,
	}
}

// Load reads configuration from a YAML file and applies environment variable overrides
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	applyEnvOverrides(cfg)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// Validate checks that all required configuration values are set
func (c *Config) Validate() error {
	if c.MetadataURL == "" {
		return fmt.Errorf("metadataURL is required")
	}
	if c.JWKSPath == "" {
		return fmt.Errorf("jwksPath is required")
	}
	if c.CachePath == "" {
		return fmt.Errorf("cachePath is required")
	}
	if c.MaxParallelChecks < 1 {
		return fmt.Errorf("maxParallelChecks must be at least 1")
	}
	if c.ChecksPerMinute < 1 {
		return fmt.Errorf("checksPerMinute must be at least 1")
	}
	if c.MinCheckInterval < time.Minute {
		return fmt.Errorf("minCheckInterval must be at least 1 minute")
	}
	if c.TLSTimeout < time.Second {
		return fmt.Errorf("tlsTimeout must be at least 1 second")
	}
	return nil
}

// applyEnvOverrides applies environment variable overrides to the config.
// Environment variables use the MATFMONITOR_ prefix with uppercase field names.
func applyEnvOverrides(cfg *Config) {
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		envName := "MATFMONITOR_" + strings.ToUpper(field.Name)
		envValue := os.Getenv(envName)

		if envValue == "" {
			continue
		}

		fieldValue := v.Field(i)
		switch fieldValue.Kind() {
		case reflect.String:
			fieldValue.SetString(envValue)
		case reflect.Int:
			var intVal int
			if _, err := fmt.Sscanf(envValue, "%d", &intVal); err == nil {
				fieldValue.SetInt(int64(intVal))
			}
		case reflect.Int64:
			// Handle time.Duration
			if field.Type == reflect.TypeOf(time.Duration(0)) {
				if duration, err := time.ParseDuration(envValue); err == nil {
					fieldValue.SetInt(int64(duration))
				}
			}
		}
	}
}
