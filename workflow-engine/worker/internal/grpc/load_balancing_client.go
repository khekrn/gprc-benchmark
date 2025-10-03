package grpc

import (
	"fmt"
	"log"
	"sync"

	"workflow-worker/internal/config"
	pb "workflow-worker/proto"
)

// LoadBalancingClient manages connections to multiple servers with load balancing
type LoadBalancingClient struct {
	config        *config.Config
	loadBalancer  *config.LoadBalancer
	clients       map[string]*Client // serverAddress -> Client
	mu            sync.RWMutex
	currentClient *Client
}

// NewLoadBalancingClient creates a new load balancing client
func NewLoadBalancingClient(cfg *config.Config) (*LoadBalancingClient, error) {
	lbc := &LoadBalancingClient{
		config:       cfg,
		loadBalancer: cfg.NewLoadBalancer(),
		clients:      make(map[string]*Client),
	}

	// Initialize connections to all servers
	err := lbc.initializeConnections()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize server connections: %v", err)
	}

	return lbc, nil
}

// initializeConnections establishes connections to all configured servers
func (lbc *LoadBalancingClient) initializeConnections() error {
	servers := lbc.loadBalancer.GetAllServers()

	for _, server := range servers {
		log.Printf("Connecting to server: %s (%s)", server.Name, server.Address)

		client, err := NewClient(server.Address)
		if err != nil {
			log.Printf("Failed to connect to server %s: %v", server.Name, err)
			continue // Continue with other servers
		}

		lbc.mu.Lock()
		lbc.clients[server.Address] = client
		lbc.mu.Unlock()

		log.Printf("Connected to server %s successfully", server.Name)
	}

	if len(lbc.clients) == 0 {
		return fmt.Errorf("failed to connect to any servers")
	}

	// Set initial current client
	lbc.setCurrentClient()

	return nil
}

// setCurrentClient sets the current client based on load balancing strategy
func (lbc *LoadBalancingClient) setCurrentClient() {
	server := lbc.loadBalancer.GetNextServer()

	lbc.mu.RLock()
	client, exists := lbc.clients[server.Address]
	lbc.mu.RUnlock()

	if exists {
		lbc.currentClient = client
		log.Printf("Switched to server: %s (%s)", server.Name, server.Address)
	}
}

// GetClient returns the current client based on load balancing
func (lbc *LoadBalancingClient) GetClient() *Client {
	lbc.setCurrentClient() // Always get next server according to strategy
	return lbc.currentClient
}

// RegisterEndpoint registers the worker endpoint with the current server
func (lbc *LoadBalancingClient) RegisterEndpoint(name, endpoint string) (*pb.RegisterEndpointResponse, error) {
	client := lbc.GetClient()
	if client == nil {
		return nil, fmt.Errorf("no available servers")
	}

	return client.RegisterEndpoint(name, endpoint)
}

// StartStream starts streams with ALL servers for true load balancing
func (lbc *LoadBalancingClient) StartStream() error {
	lbc.mu.RLock()
	defer lbc.mu.RUnlock()

	var errors []error
	for serverAddr, client := range lbc.clients {
		log.Printf("Starting stream with server: %s", serverAddr)
		err := client.StartStream()
		if err != nil {
			log.Printf("Failed to start stream with server %s: %v", serverAddr, err)
			errors = append(errors, fmt.Errorf("server %s: %v", serverAddr, err))
		} else {
			log.Printf("Stream established with server: %s", serverAddr)
		}
	}

	if len(errors) == len(lbc.clients) {
		return fmt.Errorf("failed to establish streams with any server: %v", errors)
	}

	if len(errors) > 0 {
		log.Printf("Warning: Failed to establish streams with some servers: %v", errors)
	}

	return nil
}

// SetWorkflowEngine sets the workflow engine on the current client
func (lbc *LoadBalancingClient) SetWorkflowEngine(engine WorkflowEngine) {
	// Set on all clients for consistency
	lbc.mu.RLock()
	defer lbc.mu.RUnlock()

	for _, client := range lbc.clients {
		client.SetWorkflowEngine(engine)
	}
}

// SendStateUpdate sends a state update through the current client
func (lbc *LoadBalancingClient) SendStateUpdate(workflowID int64, stateName, stateType, status string, data map[string]interface{}) error {
	client := lbc.GetClient()
	if client == nil {
		return fmt.Errorf("no available servers")
	}

	return client.SendStateUpdate(workflowID, stateName, stateType, status, data)
}

// SendWorkflowComplete sends workflow completion through the current client
func (lbc *LoadBalancingClient) SendWorkflowComplete(workflowID int64, status string, variables map[string]interface{}) error {
	client := lbc.GetClient()
	if client == nil {
		return fmt.Errorf("no available servers")
	}

	return client.SendWorkflowComplete(workflowID, status, variables)
}

// Close closes all server connections
func (lbc *LoadBalancingClient) Close() error {
	lbc.mu.Lock()
	defer lbc.mu.Unlock()

	for address, client := range lbc.clients {
		err := client.Close()
		if err != nil {
			log.Printf("Error closing connection to %s: %v", address, err)
		}
	}

	lbc.clients = make(map[string]*Client)
	lbc.currentClient = nil

	log.Printf("All server connections closed")
	return nil
}

// GetActiveConnections returns the number of active server connections
func (lbc *LoadBalancingClient) GetActiveConnections() int {
	lbc.mu.RLock()
	defer lbc.mu.RUnlock()
	return len(lbc.clients)
}
