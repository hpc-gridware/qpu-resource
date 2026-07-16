package availability

import (
	"fmt"
	"os"
	"time"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	LoadSensor LoadSensorConfig `yaml:"load_sensor"`
	Warden     WardenConfig     `yaml:"warden"`
	Static     StaticConfig     `yaml:"static"`
}

type LoadSensorConfig struct {
	Enabled           bool   `yaml:"enabled"`
	Scope             string `yaml:"scope"`
	ResourceName      string `yaml:"resource_name"`
	SlotsResourceName string `yaml:"slots_resource_name"`
	Provider          string `yaml:"provider"`
	TimeoutSeconds    int    `yaml:"timeout_seconds"`
}

type WardenConfig struct {
	BaseURL   string `yaml:"base_url"`
	Endpoint  string `yaml:"endpoint"`
	TLSVerify bool   `yaml:"tls_verify"`
}

type StaticConfig struct {
	Ready     bool   `yaml:"ready"`
	StateFile string `yaml:"state_file"`
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	cfg.ApplyDefaults()
	return cfg, nil
}

func DefaultConfig() Config {
	cfg := Config{Warden: WardenConfig{TLSVerify: true}}
	cfg.ApplyDefaults()
	return cfg
}

func (c *Config) ApplyDefaults() {
	if c.LoadSensor.Scope == "" {
		c.LoadSensor.Scope = "global"
	}
	if c.LoadSensor.ResourceName == "" {
		c.LoadSensor.ResourceName = "qpu_ready"
	}
	if c.LoadSensor.Provider == "" {
		c.LoadSensor.Provider = "static"
	}
	if c.LoadSensor.TimeoutSeconds <= 0 {
		c.LoadSensor.TimeoutSeconds = 3
	}
	if c.Warden.BaseURL == "" {
		c.Warden.BaseURL = "http://127.0.0.1:8006"
	}
	if c.Warden.Endpoint == "" {
		c.Warden.Endpoint = "/accessible"
	}
}

func (c Config) Provider() (AvailabilityProvider, time.Duration, error) {
	timeout := time.Duration(c.LoadSensor.TimeoutSeconds) * time.Second
	switch c.LoadSensor.Provider {
	case "static":
		return StaticProvider{Ready: c.Static.Ready, StateFile: c.Static.StateFile}, timeout, nil
	case "warden":
		provider := WardenProvider{
			BaseURL:   c.Warden.BaseURL,
			Endpoint:  c.Warden.Endpoint,
			TLSVerify: c.Warden.TLSVerify,
		}
		return provider.WithTimeout(timeout), timeout, nil
	default:
		return nil, timeout, fmt.Errorf("unknown availability provider %q", c.LoadSensor.Provider)
	}
}
