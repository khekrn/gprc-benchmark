// Package redis_client provides Redis connectivity for worker registration
// and discovery. It handles worker lifecycle management, heartbeats,
// and pub/sub events for worker state changes.
package redis_client

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"workflow-worker/internal/config"

	"github.com/redis/go-redis/v9"
)

// WorkerInfo represents information about a worker
type WorkerInfo struct {
	Name          string            `json:"name"`
	Endpoint      string            `json:"endpoint"`
	Status        string            `json:"status"` // "online", "offline"
	LastSeen      time.Time         `json:"last_seen"`
	WorkflowTypes []string          `json:"workflow_types"` // Workflows this worker can handle
	Capacity      string            `json:"capacity"`       // Worker capacity
	Metadata      map[string]string `json:"metadata"`
}

// WorkerEvent represents a worker event for pub/sub
type WorkerEvent struct {
	Type      string     `json:"type"` // "worker_online", "worker_offline"
	Worker    WorkerInfo `json:"worker"`
	Timestamp time.Time  `json:"timestamp"`
}

// Client represents a Redis client for worker discovery
type Client struct {
	client *redis.Client
	config *config.RedisConfig
	ctx    context.Context
	cancel context.CancelFunc
}

// NewClient creates a new Redis client
func NewClient(cfg *config.RedisConfig) (*Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	// Test connection
	ctx, cancel := context.WithCancel(context.Background())
	_, err := client.Ping(ctx).Result()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to connect to Redis: %v", err)
	}

	log.Printf("Connected to Redis at %s:%d", cfg.Host, cfg.Port)

	return &Client{
		client: client,
		config: cfg,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

// RegisterWorker registers a worker in Redis
func (c *Client) RegisterWorker(worker WorkerInfo) error {
	worker.Status = "online"
	worker.LastSeen = time.Now()

	data, err := json.Marshal(worker)
	if err != nil {
		return fmt.Errorf("failed to marshal worker info: %v", err)
	}

	// Store worker info in Redis hash
	err = c.client.HSet(c.ctx, c.config.WorkerRegistryKey, worker.Name, data).Err()
	if err != nil {
		return fmt.Errorf("failed to register worker in Redis: %v", err)
	}

	// Publish worker online event
	event := WorkerEvent{
		Type:      "worker_online",
		Worker:    worker,
		Timestamp: time.Now(),
	}

	eventData, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal worker event: %v", err)
	}

	err = c.client.Publish(c.ctx, c.config.WorkerEventsChannel, eventData).Err()
	if err != nil {
		return fmt.Errorf("failed to publish worker online event: %v", err)
	}

	log.Printf("Worker registered and online event published: %s at %s", worker.Name, worker.Endpoint)
	return nil
}

// UnregisterWorker removes a worker from Redis
func (c *Client) UnregisterWorker(workerName string) error {
	// Get worker info before removing
	data, err := c.client.HGet(c.ctx, c.config.WorkerRegistryKey, workerName).Result()
	if err != nil {
		if err == redis.Nil {
			log.Printf("Worker %s not found in registry", workerName)
			return nil
		}
		return fmt.Errorf("failed to get worker info: %v", err)
	}

	var worker WorkerInfo
	err = json.Unmarshal([]byte(data), &worker)
	if err != nil {
		return fmt.Errorf("failed to unmarshal worker info: %v", err)
	}

	// Remove from registry
	err = c.client.HDel(c.ctx, c.config.WorkerRegistryKey, workerName).Err()
	if err != nil {
		return fmt.Errorf("failed to unregister worker: %v", err)
	}

	// Publish worker offline event
	worker.Status = "offline"
	event := WorkerEvent{
		Type:      "worker_offline",
		Worker:    worker,
		Timestamp: time.Now(),
	}

	eventData, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal worker event: %v", err)
	}

	err = c.client.Publish(c.ctx, c.config.WorkerEventsChannel, eventData).Err()
	if err != nil {
		return fmt.Errorf("failed to publish worker offline event: %v", err)
	}

	log.Printf("Worker unregistered and offline event published: %s", workerName)
	return nil
}

// StartHeartbeat starts a heartbeat goroutine to keep the worker alive in Redis
func (c *Client) StartHeartbeat(workerName string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				err := c.updateWorkerHeartbeat(workerName)
				if err != nil {
					log.Printf("Failed to update worker heartbeat: %v", err)
				}
			case <-c.ctx.Done():
				return
			}
		}
	}()
}

// updateWorkerHeartbeat updates the last seen timestamp for a worker
func (c *Client) updateWorkerHeartbeat(workerName string) error {
	data, err := c.client.HGet(c.ctx, c.config.WorkerRegistryKey, workerName).Result()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("worker %s not found", workerName)
		}
		return fmt.Errorf("failed to get worker info: %v", err)
	}

	var worker WorkerInfo
	err = json.Unmarshal([]byte(data), &worker)
	if err != nil {
		return fmt.Errorf("failed to unmarshal worker info: %v", err)
	}

	worker.LastSeen = time.Now()

	updatedData, err := json.Marshal(worker)
	if err != nil {
		return fmt.Errorf("failed to marshal updated worker info: %v", err)
	}

	err = c.client.HSet(c.ctx, c.config.WorkerRegistryKey, workerName, updatedData).Err()
	if err != nil {
		return fmt.Errorf("failed to update worker heartbeat: %v", err)
	}

	return nil
}

// Close closes the Redis client
func (c *Client) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	return c.client.Close()
}
