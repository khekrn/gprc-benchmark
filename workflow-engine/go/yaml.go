package main

import (
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v2"
)

// Minimal config structures for testing
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Logging  LoggingConfig  `yaml:"logging"`
}

type ServerConfig struct {
	HTTP HTTPConfig `yaml:"http"`
	GRPC GRPCConfig `yaml:"grpc"`
	Name string     `yaml:"name"`
}

type HTTPConfig struct {
	Port int `yaml:"port"`
}

type GRPCConfig struct {
	Port int `yaml:"port"`
}

type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbname"`
	SSLMode  string `yaml:"sslmode"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("Usage: go run yaml_test.go <config-file>")
	}

	configPath := os.Args[1]
	fmt.Printf("Loading config from: %s\n", configPath)

	// Read file
	configFile, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("Failed to read config file: %v", err)
	}

	fmt.Printf("File contents:\n%s\n", string(configFile))

	// Parse YAML
	var config Config
	err = yaml.Unmarshal(configFile, &config)
	if err != nil {
		log.Fatalf("Failed to parse config file: %v", err)
	}

	fmt.Printf("Parsed config:\n")
	fmt.Printf("  Server Name: %s\n", config.Server.Name)
	fmt.Printf("  HTTP Port: %d\n", config.Server.HTTP.Port)
	fmt.Printf("  gRPC Port: %d\n", config.Server.GRPC.Port)
}
