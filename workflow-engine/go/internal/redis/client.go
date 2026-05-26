package redis_client

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"workflow-engine/internal/config"

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
	pubSub *redis.PubSub
}

// NewClient creates a new Redis client
func NewClient(cfg *config.RedisConfig) (*Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	// Test connection
	ctx := context.Background()
	_, err := client.Ping(ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %v", err)
	}

	log.Printf("Connected to Redis at %s:%d", cfg.Host, cfg.Port)

	return &Client{
		client: client,
		config: cfg,
		ctx:    ctx,
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

// GetAllWorkers returns all registered workers
func (c *Client) GetAllWorkers() (map[string]WorkerInfo, error) {
	workers := make(map[string]WorkerInfo)

	result, err := c.client.HGetAll(c.ctx, c.config.WorkerRegistryKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get workers: %v", err)
	}

	for name, data := range result {
		var worker WorkerInfo
		err := json.Unmarshal([]byte(data), &worker)
		if err != nil {
			log.Printf("Failed to unmarshal worker %s: %v", name, err)
			continue
		}
		workers[name] = worker
	}

	return workers, nil
}

// GetWorkersForWorkflow returns workers that can handle a specific workflow
func (c *Client) GetWorkersForWorkflow(workflowName string) ([]WorkerInfo, error) {
	allWorkers, err := c.GetAllWorkers()
	if err != nil {
		return nil, err
	}

	var workflowWorkers []WorkerInfo
	for _, worker := range allWorkers {
		// Check if worker is online and can handle this workflow
		if worker.Status == "online" {
			for _, wType := range worker.WorkflowTypes {
				if wType == workflowName || wType == "*" { // "*" means can handle any workflow
					workflowWorkers = append(workflowWorkers, worker)
					break
				}
			}
		}
	}

	return workflowWorkers, nil
}

// SubscribeToWorkerEvents subscribes to worker events
func (c *Client) SubscribeToWorkerEvents() <-chan WorkerEvent {
	c.pubSub = c.client.Subscribe(c.ctx, c.config.WorkerEventsChannel)
	eventCh := make(chan WorkerEvent, 100)

	go func() {
		defer close(eventCh)
		defer c.pubSub.Close()

		ch := c.pubSub.Channel()
		for msg := range ch {
			var event WorkerEvent
			err := json.Unmarshal([]byte(msg.Payload), &event)
			if err != nil {
				log.Printf("Failed to unmarshal worker event: %v", err)
				continue
			}

			select {
			case eventCh <- event:
			case <-c.ctx.Done():
				return
			}
		}
	}()

	return eventCh
}

// UpdateWorkerHeartbeat updates the last seen timestamp for a worker
func (c *Client) UpdateWorkerHeartbeat(workerName string) error {
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
	if c.pubSub != nil {
		c.pubSub.Close()
	}
	return c.client.Close()
}
