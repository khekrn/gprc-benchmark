package config

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"gopkg.in/yaml.v2"
)

// Config holds the server configuration
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Redis    RedisConfig    `yaml:"redis"`
	Logging  LoggingConfig  `yaml:"logging"`
}

// ServerConfig holds server-specific configuration
type ServerConfig struct {
	HTTP HTTPConfig `yaml:"http"`
	GRPC GRPCConfig `yaml:"grpc"`
	Name string     `yaml:"name"`
}

// HTTPConfig holds HTTP server configuration
type HTTPConfig struct {
	Port int `yaml:"port"`
}

// GRPCConfig holds gRPC server configuration
type GRPCConfig struct {
	Port int `yaml:"port"`
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbname"`
	SSLMode  string `yaml:"sslmode"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level string `yaml:"level"`
}

// RedisConfig holds Redis configuration
type RedisConfig struct {
	Host                string `yaml:"host"`
	Port                int    `yaml:"port"`
	Password            string `yaml:"password"`
	DB                  int    `yaml:"db"`
	WorkerRegistryKey   string `yaml:"worker_registry_key"`
	WorkerEventsChannel string `yaml:"worker_events_channel"`
}

// LoadConfig loads configuration from file with environment variable overrides
func LoadConfig(configPath string) (*Config, error) {
	// Set default config path if not provided
	if configPath == "" {
		configPath = "config/config.yaml"
	}

	// Read config file
	configFile, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}

	// Parse YAML
	var config Config
	err = yaml.Unmarshal(configFile, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file: %v", err)
	}

	// Override with environment variables if present
	overrideWithEnv(&config)

	log.Printf("Loaded config: Server=%s HTTP=:%d gRPC=:%d",
		config.Server.Name, config.Server.HTTP.Port, config.Server.GRPC.Port)

	return &config, nil
}

// overrideWithEnv overrides config values with environment variables
func overrideWithEnv(config *Config) {
	if port := os.Getenv("HTTP_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			config.Server.HTTP.Port = p
		}
	}

	if port := os.Getenv("GRPC_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			config.Server.GRPC.Port = p
		}
	}

	if name := os.Getenv("SERVER_NAME"); name != "" {
		config.Server.Name = name
	}

	if host := os.Getenv("DB_HOST"); host != "" {
		config.Database.Host = host
	}

	if port := os.Getenv("DB_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			config.Database.Port = p
		}
	}

	if user := os.Getenv("DB_USER"); user != "" {
		config.Database.User = user
	}

	if password := os.Getenv("DB_PASSWORD"); password != "" {
		config.Database.Password = password
	}

	if dbname := os.Getenv("DB_NAME"); dbname != "" {
		config.Database.DBName = dbname
	}
}

// GetHTTPAddress returns the HTTP server address
func (c *Config) GetHTTPAddress() string {
	return fmt.Sprintf(":%d", c.Server.HTTP.Port)
}

// GetGRPCAddress returns the gRPC server address
func (c *Config) GetGRPCAddress() string {
	return fmt.Sprintf(":%d", c.Server.GRPC.Port)
}

// GetDatabaseDSN returns the database connection string
func (c *Config) GetDatabaseDSN() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Database.Host, c.Database.Port, c.Database.User,
		c.Database.Password, c.Database.DBName, c.Database.SSLMode)
}
