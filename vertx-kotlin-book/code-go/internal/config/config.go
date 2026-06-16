// Package config loads strongly-typed configuration from a YAML file with
// environment-variable overrides, mirroring the layered Kotlin AppConfig.
//
// Layers (later overrides earlier):
//  1. config.yaml (path from CONFIG_FILE, default ./config.yaml)
//  2. environment variables (HTTP_PORT, GRPC_PORT, DB_HOST, ...)
package config

import (
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	HTTP                  HTTP  `yaml:"http"`
	GRPC                  GRPC  `yaml:"grpc"`
	DB                    DB    `yaml:"db"`
	ShutdownGracePeriodMs int64 `yaml:"shutdownGracePeriodMs"`
}

type HTTP struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type GRPC struct {
	Port int `yaml:"port"`
}

type DB struct {
	Host            string `yaml:"host"`
	Port            int    `yaml:"port"`
	Database        string `yaml:"database"`
	User            string `yaml:"user"`
	Password        string `yaml:"password"`
	PoolMaxSize     int32  `yaml:"poolMaxSize"`
	SchemaOnStartup bool   `yaml:"schemaOnStartup"`
}

// Defaults match the Kotlin application.yaml.
func defaults() Config {
	return Config{
		HTTP: HTTP{Host: "0.0.0.0", Port: 8080},
		GRPC: GRPC{Port: 9090},
		DB: DB{
			Host:            "localhost",
			Port:            5432,
			Database:        "appdb",
			User:            "app",
			Password:        "app",
			PoolMaxSize:     16,
			SchemaOnStartup: true,
		},
		ShutdownGracePeriodMs: 15000,
	}
}

// Load reads the YAML file (if present) on top of the defaults, then applies
// environment-variable overrides.
func Load() (Config, error) {
	cfg := defaults()

	path := getenv("CONFIG_FILE", "config.yaml")
	if raw, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return cfg, err
		}
	} else if !os.IsNotExist(err) {
		return cfg, err
	}

	// Env overrides.
	cfg.HTTP.Host = getenv("HTTP_HOST", cfg.HTTP.Host)
	cfg.HTTP.Port = getenvInt("HTTP_PORT", cfg.HTTP.Port)
	cfg.GRPC.Port = getenvInt("GRPC_PORT", cfg.GRPC.Port)
	cfg.DB.Host = getenv("DB_HOST", cfg.DB.Host)
	cfg.DB.Port = getenvInt("DB_PORT", cfg.DB.Port)
	cfg.DB.Database = getenv("DB_DATABASE", cfg.DB.Database)
	cfg.DB.User = getenv("DB_USER", cfg.DB.User)
	cfg.DB.Password = getenv("DB_PASSWORD", cfg.DB.Password)
	cfg.DB.PoolMaxSize = int32(getenvInt("DB_POOL_MAX_SIZE", int(cfg.DB.PoolMaxSize)))

	return cfg, nil
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
