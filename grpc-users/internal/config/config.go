package config

import (
	"log"

	"github.com/spf13/viper"
)

type Config struct {
	Database  DatabaseConfig  `mapstructure:"database"`
	Server    ServerConfig    `mapstructure:"server"`
	Logger    LoggerConfig    `mapstructure:"logger"`
	Migration MigrationConfig `mapstructure:"migration"`
}

type DatabaseConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
	DBName   string `mapstructure:"dbname"`
	Schema   string `mapstructure:"schema"`
	SSLMode  string `mapstructure:"sslmode"`
}

type ServerConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	GRPCPort int    `mapstructure:"grpc_port"`
}

type LoggerConfig struct {
	Level string `mapstructure:"level"`
}

type MigrationConfig struct {
	AutoMigrate   bool `mapstructure:"auto_migrate"`
	MaxRetries    int  `mapstructure:"max_retries"`
	RetryInterval int  `mapstructure:"retry_interval_seconds"`
}

func LoadConfig(path string) (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(path)
	viper.AddConfigPath(".")

	// Set default values
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", 5432)
	viper.SetDefault("database.user", "postgres")
	viper.SetDefault("database.password", "sam")
	viper.SetDefault("database.dbname", "proddb")
	viper.SetDefault("database.schema", "users")
	viper.SetDefault("database.sslmode", "disable")
	viper.SetDefault("server.host", "localhost")
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.grpc_port", 9090)
	viper.SetDefault("logger.level", "info")
	viper.SetDefault("migration.auto_migrate", true)
	viper.SetDefault("migration.max_retries", 10)
	viper.SetDefault("migration.retry_interval_seconds", 3)

	// Enable environment variables
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		log.Printf("Error reading config file: %v", err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, err
	}

	return &config, nil
}
