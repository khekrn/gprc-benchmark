package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	benchmarkpb "github.com/benchmark/golang"
	"github.com/benchmark/golang/internal/db"
	"github.com/benchmark/golang/internal/metrics"
)

type BenchmarkServer struct {
	benchmarkpb.UnimplementedBenchmarkServiceServer
	db         *db.DB
	serverType string
	mu         sync.RWMutex
	clients    map[string]*clientConnection
}

type clientConnection struct {
	id        string
	stream    benchmarkpb.BenchmarkService_ProcessStreamingServer
	active    bool
	createdAt time.Time
}

func NewBenchmarkServer(database *db.DB) *BenchmarkServer {
	return &BenchmarkServer{
		db:         database,
		serverType: "golang",
		clients:    make(map[string]*clientConnection),
	}
}

func (s *BenchmarkServer) ProcessUnary(ctx context.Context, req *benchmarkpb.UnaryRequest) (*benchmarkpb.UnaryResponse, error) {
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		metrics.RecordRequest("ProcessUnary", "success", s.serverType, duration)
	}()

	// Record payload size
	payloadSize := len(req.Message.Payload)
	metrics.RecordMessageSize("unary", s.serverType, payloadSize)

	// Simulate processing time proportional to payload size
	processingDelay := time.Duration(payloadSize/1024) * time.Millisecond
	time.Sleep(processingDelay)

	response := &benchmarkpb.UnaryResponse{
		Id:               uuid.New().String(),
		Status:           "processed",
		ProcessedAt:      timestamppb.New(time.Now()),
		ProcessingTimeMs: time.Since(startTime).Milliseconds(),
		ServerType:       s.serverType,
		SavedToDb:        false,
	}

	// Save to database if requested
	if req.SaveToDb && s.db != nil {
		dbStart := time.Now()

		record := &db.BenchmarkRecord{
			ID:            response.Id,
			Timestamp:     req.Message.Timestamp.AsTime(),
			OperationType: "unary",
			ServerType:    s.serverType,
			ClientID:      req.Message.Metadata.ClientId,
			SequenceNum:   req.Message.Metadata.SequenceNumber,
			CorrelationID: req.Message.Metadata.CorrelationId,
			PayloadSize:   payloadSize,
			ProcessingMS:  response.ProcessingTimeMs,
			Headers:       db.Headers(req.Message.Metadata.Headers),
		}

		if err := s.db.SaveBenchmarkRecord(ctx, record); err != nil {
			log.Printf("Failed to save benchmark record: %v", err)
			metrics.RecordDatabaseOperation("save", "error", s.serverType, time.Since(dbStart))
		} else {
			response.SavedToDb = true
			metrics.RecordDatabaseOperation("save", "success", s.serverType, time.Since(dbStart))
		}
	}

	return response, nil
}

func (s *BenchmarkServer) ProcessStreaming(stream benchmarkpb.BenchmarkService_ProcessStreamingServer) error {
	clientID := uuid.New().String()

	metrics.IncrementActiveConnections(s.serverType)
	defer metrics.DecrementActiveConnections(s.serverType)

	// Register client connection
	s.mu.Lock()
	conn := &clientConnection{
		id:        clientID,
		stream:    stream,
		active:    true,
		createdAt: time.Now(),
	}
	s.clients[clientID] = conn
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, clientID)
		s.mu.Unlock()
	}()

	log.Printf("Started streaming connection for client: %s", clientID)

	for {
		startTime := time.Now()

		req, err := stream.Recv()
		if err == io.EOF {
			log.Printf("Client %s closed the stream", clientID)
			return nil
		}
		if err != nil {
			log.Printf("Error receiving from stream: %v", err)
			metrics.RecordRequest("ProcessStreaming", "error", s.serverType, time.Since(startTime))
			return status.Error(codes.Internal, "stream receive error")
		}

		// Record payload size
		payloadSize := len(req.Message.Payload)
		metrics.RecordMessageSize("streaming", s.serverType, payloadSize)

		// Simulate processing
		processingDelay := time.Duration(payloadSize/2048) * time.Millisecond
		time.Sleep(processingDelay)

		response := &benchmarkpb.StreamingResponse{
			Id:               uuid.New().String(),
			Status:           "processed",
			ProcessedAt:      timestamppb.New(time.Now()),
			ProcessingTimeMs: time.Since(startTime).Milliseconds(),
			ServerType:       s.serverType,
			BatchCount:       req.BatchSize,
			SavedToDb:        false,
		}

		// Save to database if requested
		if req.SaveToDb && s.db != nil {
			dbStart := time.Now()

			record := &db.BenchmarkRecord{
				ID:            response.Id,
				Timestamp:     req.Message.Timestamp.AsTime(),
				OperationType: "streaming",
				ServerType:    s.serverType,
				ClientID:      req.Message.Metadata.ClientId,
				SequenceNum:   req.Message.Metadata.SequenceNumber,
				CorrelationID: req.Message.Metadata.CorrelationId,
				PayloadSize:   payloadSize,
				ProcessingMS:  response.ProcessingTimeMs,
				Headers:       db.Headers(req.Message.Metadata.Headers),
			}

			if err := s.db.SaveBenchmarkRecord(stream.Context(), record); err != nil {
				log.Printf("Failed to save streaming record: %v", err)
				metrics.RecordDatabaseOperation("save", "error", s.serverType, time.Since(dbStart))
			} else {
				response.SavedToDb = true
				metrics.RecordDatabaseOperation("save", "success", s.serverType, time.Since(dbStart))
			}
		}

		if err := stream.Send(response); err != nil {
			log.Printf("Error sending response: %v", err)
			metrics.RecordRequest("ProcessStreaming", "error", s.serverType, time.Since(startTime))
			return status.Error(codes.Internal, "stream send error")
		}

		metrics.RecordRequest("ProcessStreaming", "success", s.serverType, time.Since(startTime))
	}
}

func (s *BenchmarkServer) HealthCheck(ctx context.Context, req *benchmarkpb.HealthRequest) (*benchmarkpb.HealthResponse, error) {
	return &benchmarkpb.HealthResponse{
		Status:     "healthy",
		Timestamp:  timestamppb.New(time.Now()),
		ServerType: s.serverType,
	}, nil
}

// GetActiveConnections returns the number of active streaming connections
func (s *BenchmarkServer) GetActiveConnections() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}

// Generate4KBPayload generates a 4KB payload for testing
func Generate4KBPayload() []byte {
	payload := make([]byte, 4096) // 4KB
	_, err := rand.Read(payload)
	if err != nil {
		// Fallback to deterministic data
		for i := range payload {
			payload[i] = byte(i % 256)
		}
	}
	return payload
}

// GenerateTestMessage creates a test message with 4KB payload
func GenerateTestMessage(clientID string, sequenceNum int64, operationType string) *benchmarkpb.BenchmarkMessage {
	correlationID := uuid.New().String()

	return &benchmarkpb.BenchmarkMessage{
		Id:            uuid.New().String(),
		Timestamp:     timestamppb.New(time.Now()),
		OperationType: operationType,
		Payload:       Generate4KBPayload(),
		Metadata: &benchmarkpb.MessageMetadata{
			ClientId:       clientID,
			ServerType:     "client",
			SequenceNumber: sequenceNum,
			CorrelationId:  correlationID,
			Headers: map[string]string{
				"test-header":     "benchmark",
				"client-version":  "1.0.0",
				"correlation-id":  correlationID,
				"sequence-number": fmt.Sprintf("%d", sequenceNum),
			},
		},
		DataPoints: []*benchmarkpb.DataPoint{
			{
				Key:          "cpu_usage",
				Value:        "45.2",
				NumericValue: 45.2,
				CreatedAt:    timestamppb.New(time.Now()),
			},
			{
				Key:          "memory_usage",
				Value:        "67.8",
				NumericValue: 67.8,
				CreatedAt:    timestamppb.New(time.Now()),
			},
		},
	}
}
