package config

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v2"
)

// Config holds the worker configuration
type Config struct {
	Worker  WorkerConfig  `yaml:"worker"`
	Redis   RedisConfig   `yaml:"redis"`
	Servers ServersConfig `yaml:"servers"`
	Logging LoggingConfig `yaml:"logging"`
}

// WorkerConfig holds worker-specific configuration
type WorkerConfig struct {
	Name     string `yaml:"name"`
	Endpoint string `yaml:"endpoint"`
	Port     string `yaml:"port"`
}

// ServersConfig holds server endpoints and load balancing configuration
type ServersConfig struct {
	Endpoints     []ServerEndpoint    `yaml:"endpoints"`
	LoadBalancing LoadBalancingConfig `yaml:"load_balancing"`
}

// ServerEndpoint represents a server endpoint
type ServerEndpoint struct {
	Address string `yaml:"address"`
	Name    string `yaml:"name"`
	Weight  int    `yaml:"weight"`
}

// LoadBalancingConfig holds load balancing settings
type LoadBalancingConfig struct {
	Strategy            string `yaml:"strategy"`
	HealthCheckInterval string `yaml:"health_check_interval"`
	ConnectionTimeout   string `yaml:"connection_timeout"`
	RetryAttempts       int    `yaml:"retry_attempts"`
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

// LoadBalancer handles client-side load balancing
type LoadBalancer struct {
	endpoints    []ServerEndpoint
	currentIndex int
	mu           sync.RWMutex
	strategy     string
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

	log.Printf("Loaded worker config: Name=%s Endpoint=%s",
		config.Worker.Name, config.Worker.Endpoint)
	log.Printf("Server endpoints: %d servers with %s load balancing",
		len(config.Servers.Endpoints), config.Servers.LoadBalancing.Strategy)

	return &config, nil
}

// overrideWithEnv overrides config values with environment variables
func overrideWithEnv(config *Config) {
	if name := os.Getenv("WORKER_NAME"); name != "" {
		config.Worker.Name = name
	}

	if endpoint := os.Getenv("WORKER_ENDPOINT"); endpoint != "" {
		config.Worker.Endpoint = endpoint
	}

	if port := os.Getenv("WORKER_PORT"); port != "" {
		config.Worker.Port = port
	}
}

// NewLoadBalancer creates a new load balancer with the given configuration
func (c *Config) NewLoadBalancer() *LoadBalancer {
	return &LoadBalancer{
		endpoints: c.Servers.Endpoints,
		strategy:  c.Servers.LoadBalancing.Strategy,
	}
}

// GetNextServer returns the next server according to the load balancing strategy
func (lb *LoadBalancer) GetNextServer() ServerEndpoint {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	if len(lb.endpoints) == 0 {
		return ServerEndpoint{}
	}

	switch lb.strategy {
	case "round_robin":
		server := lb.endpoints[lb.currentIndex]
		lb.currentIndex = (lb.currentIndex + 1) % len(lb.endpoints)
		return server
	case "random":
		// Simple random selection for now
		idx := time.Now().UnixNano() % int64(len(lb.endpoints))
		return lb.endpoints[idx]
	default:
		// Default to round robin
		server := lb.endpoints[lb.currentIndex]
		lb.currentIndex = (lb.currentIndex + 1) % len(lb.endpoints)
		return server
	}
}

// GetAllServers returns all configured server endpoints
func (lb *LoadBalancer) GetAllServers() []ServerEndpoint {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	return lb.endpoints
}

// GetServerAddress returns the server address for connection
func (c *Config) GetServerAddress() string {
	// For backward compatibility, return the first server if available
	if len(c.Servers.Endpoints) > 0 {
		return c.Servers.Endpoints[0].Address
	}
	return "localhost:9090"
}
