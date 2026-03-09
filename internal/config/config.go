package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

type RouteConfig struct {
	Subdomain string `mapstructure:"subdomain"`
	Target    int    `mapstructure:"target"`
}

type Config struct {
	ListenPort int           `mapstructure:"listen_port"`
	BaseDomain string        `mapstructure:"base_domain"`
	Daemon     bool          `mapstructure:"daemon"`
	Routes     []RouteConfig `mapstructure:"routes"`
}

func Load() (*Config, error) {
	v := viper.New()

	v.SetDefault("listen_port", 8080)
	v.SetDefault("base_domain", ".localhost")
	v.SetDefault("daemon", false)

	v.SetConfigName("botafoc")
	v.SetConfigType("yaml")

	if envConfig := os.Getenv("BOTAFOC_CONFIG"); envConfig != "" {
		v.SetConfigFile(envConfig)
	} else {
		configDir, err := os.UserConfigDir()
		if err == nil {
			v.AddConfigPath(filepath.Join(configDir, "botafoc"))
		}
	}

	v.SetEnvPrefix("BOTAFOC")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Missing config is fine — just use defaults.
	_ = v.ReadInConfig()

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	return &cfg, nil
}
