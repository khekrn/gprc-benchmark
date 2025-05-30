package service

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/benchmark/internal/db"
	"github.com/benchmark/internal/metrics"
	"github.com/benchmark/proto"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type BenchmarkService struct {
	proto.UnimplementedStreamingBenchmarkServiceServer
	db              *db.DB
	concurrentCount int64
}

func NewBenchmarkService(database *db.DB) *BenchmarkService {
	return &BenchmarkService{
		db: database,
	}
}

// generateRecordID creates a unique record ID
func generateRecordID(method string, sequenceID int32) string {
	return fmt.Sprintf("%s_%d_%d", method, sequenceID, time.Now().UnixNano())
}

// recordRequestMetrics handles common request metrics recording
func (s *BenchmarkService) recordRequestMetrics(ctx context.Context, record *db.BenchmarkRecord) (string, error) {
	start := time.Now()
	
	recordID := generateRecordID(record.RequestType, *record.SequenceID)
	record.RecordID = recordID
	record.ProcessingTimeNs = time.Since(start).Nanoseconds()
	record.ConcurrentRequests = int32(atomic.LoadInt64(&s.concurrentCount))
	
	// Record to database
	dbStart := time.Now()
	err := s.db.InsertBenchmarkRecord(ctx, record)
	dbDuration := time.Since(dbStart)
	
	if err != nil {
		metrics.RecordDatabaseOperation("insert_benchmark_record", "error", dbDuration)
		log.Error().Err(err).Str("record_id", recordID).Msg("Failed to insert benchmark record")
		return recordID, err
	}
	
	metrics.RecordDatabaseOperation("insert_benchmark_record", "success", dbDuration)
	return recordID, nil
}

// Echo implements unary RPC
func (s *BenchmarkService) Echo(ctx context.Context, req *proto.EchoRequest) (*proto.EchoResponse, error) {
	start := time.Now()
	atomic.AddInt64(&s.concurrentCount, 1)
	defer atomic.AddInt64(&s.concurrentCount, -1)

	// Update concurrent requests metric
	metrics.SetConcurrentRequests(int(atomic.LoadInt64(&s.concurrentCount)))

	// Create database record
	record := &db.BenchmarkRecord{
		RequestType:      "Echo",
		SequenceID:       &[]int32{0}[0], // Default sequence ID for unary calls
		UserID:           "echo_user",
		SessionID:        fmt.Sprintf("echo_session_%d", time.Now().Unix()),
		Payload:          req.Message,
		Metadata:         map[string]string{"timestamp": fmt.Sprintf("%d", req.Timestamp)},
		QueueTimeNs:      0, // No queue time for direct processing
	}

	recordID, err := s.recordRequestMetrics(ctx, record)
	
	// Prepare response
	resp := &proto.EchoResponse{
		Message:         fmt.Sprintf("Echo: %s", req.Message),
		Timestamp:       req.Timestamp,
		ServerTimestamp: time.Now().UnixNano(),
	}

	duration := time.Since(start)
	
	if err != nil {
		metrics.RecordUnaryRequest("Echo", "error", duration)
		return resp, status.Errorf(codes.Internal, "database error: %v", err)
	}

	metrics.RecordUnaryRequest("Echo", "success", duration)
	metrics.RecordMessageProcessed("Echo", "NORMAL", len(req.Message))
	
	log.Info().
		Str("record_id", recordID).
		Str("method", "Echo").
		Dur("duration", duration).
		Msg("Echo request processed")

	return resp, nil
}

// ClientStream implements client streaming RPC
func (s *BenchmarkService) ClientStream(stream proto.StreamingBenchmarkService_ClientStreamServer) error {
	start := time.Now()
	atomic.AddInt64(&s.concurrentCount, 1)
	defer atomic.AddInt64(&s.concurrentCount, -1)
	
	metrics.IncActiveStreams("ClientStream", "client")
	defer metrics.DecActiveStreams("ClientStream", "client")

	var requests []*proto.StreamRequest
	var totalPayloadSize int
	sessionID := fmt.Sprintf("client_stream_%d", time.Now().UnixNano())

	// Receive all requests from client
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			duration := time.Since(start)
			metrics.RecordStreamingRequest("ClientStream", "client", "error", duration)
			log.Error().Err(err).Msg("Error receiving from client stream")
			return status.Errorf(codes.Internal, "stream receive error: %v", err)
		}

		requests = append(requests, req)
		totalPayloadSize += len(req.Payload)

		// Record each request to database
		record := &db.BenchmarkRecord{
			RequestType:        "ClientStream",
			SequenceID:         &req.SequenceId,
			UserID:             req.UserId,
			SessionID:          sessionID,
			Payload:            req.Payload,
			Metadata:           req.Metadata,
			QueueTimeNs:        0,
			ConcurrentRequests: int32(atomic.LoadInt64(&s.concurrentCount)),
		}

		recordID, err := s.recordRequestMetrics(stream.Context(), record)
		if err != nil {
			log.Error().Err(err).Str("record_id", recordID).Msg("Failed to record client stream request")
		}

		priority := "NORMAL"
		if req.Type == proto.MessageType_HIGH_PRIORITY {
			priority = "HIGH_PRIORITY"
		} else if req.Type == proto.MessageType_BATCH {
			priority = "BATCH"
		}
		metrics.RecordMessageProcessed("ClientStream", priority, len(req.Payload))
	}

	// Send response
	response := &proto.StreamResponse{
		SequenceId: int32(len(requests)),
		Result:     fmt.Sprintf("Processed %d requests, total payload: %d bytes", len(requests), totalPayloadSize),
		Timestamp:  time.Now().UnixNano(),
		Stats: &proto.ProcessingStats{
			ProcessingTimeNs:    time.Since(start).Nanoseconds(),
			QueueTimeNs:         0,
			ConcurrentRequests:  int32(atomic.LoadInt64(&s.concurrentCount)),
		},
		DatabaseSuccess: true,
		ApiResponse:     "ClientStream completed successfully",
	}

	duration := time.Since(start)
	metrics.RecordStreamingRequest("ClientStream", "client", "success", duration)
	
	log.Info().
		Str("session_id", sessionID).
		Int("request_count", len(requests)).
		Dur("duration", duration).
		Msg("Client stream processed")

	return stream.SendAndClose(response)
}

// ServerStream implements server streaming RPC
func (s *BenchmarkService) ServerStream(req *proto.StreamRequest, stream proto.StreamingBenchmarkService_ServerStreamServer) error {
	start := time.Now()
	atomic.AddInt64(&s.concurrentCount, 1)
	defer atomic.AddInt64(&s.concurrentCount, -1)
	
	metrics.IncActiveStreams("ServerStream", "server")
	defer metrics.DecActiveStreams("ServerStream", "server")

	sessionID := fmt.Sprintf("server_stream_%d", time.Now().UnixNano())

	// Record the initial request
	record := &db.BenchmarkRecord{
		RequestType:        "ServerStream",
		SequenceID:         &req.SequenceId,
		UserID:             req.UserId,
		SessionID:          sessionID,
		Payload:            req.Payload,
		Metadata:           req.Metadata,
		QueueTimeNs:        0,
		ConcurrentRequests: int32(atomic.LoadInt64(&s.concurrentCount)),
	}

	recordID, err := s.recordRequestMetrics(stream.Context(), record)
	if err != nil {
		duration := time.Since(start)
		metrics.RecordStreamingRequest("ServerStream", "server", "error", duration)
		return status.Errorf(codes.Internal, "database error: %v", err)
	}

	// Send multiple responses (simulate processing and sending chunks)
	responseCount := 5 // Send 5 responses for demo
	for i := 0; i < responseCount; i++ {
		response := &proto.StreamResponse{
			SequenceId: int32(i),
			Result:     fmt.Sprintf("Server response %d for: %s", i+1, req.Payload),
			Timestamp:  time.Now().UnixNano(),
			Stats: &proto.ProcessingStats{
				ProcessingTimeNs:   time.Since(start).Nanoseconds(),
				QueueTimeNs:        0,
				ConcurrentRequests: int32(atomic.LoadInt64(&s.concurrentCount)),
			},
			RecordId:        recordID,
			DatabaseSuccess: true,
			ApiResponse:     fmt.Sprintf("ServerStream response %d", i+1),
		}

		if err := stream.Send(response); err != nil {
			duration := time.Since(start)
			metrics.RecordStreamingRequest("ServerStream", "server", "error", duration)
			log.Error().Err(err).Int("response_num", i).Msg("Error sending server stream response")
			return status.Errorf(codes.Internal, "stream send error: %v", err)
		}

		// Simulate some processing time
		time.Sleep(100 * time.Millisecond)
	}

	duration := time.Since(start)
	metrics.RecordStreamingRequest("ServerStream", "server", "success", duration)
	
	priority := "NORMAL"
	if req.Type == proto.MessageType_HIGH_PRIORITY {
		priority = "HIGH_PRIORITY"
	} else if req.Type == proto.MessageType_BATCH {
		priority = "BATCH"
	}
	metrics.RecordMessageProcessed("ServerStream", priority, len(req.Payload))
	
	log.Info().
		Str("record_id", recordID).
		Str("session_id", sessionID).
		Int("response_count", responseCount).
		Dur("duration", duration).
		Msg("Server stream processed")

	return nil
}

// BidirectionalStream implements bidirectional streaming RPC
func (s *BenchmarkService) BidirectionalStream(stream proto.StreamingBenchmarkService_BidirectionalStreamServer) error {
	start := time.Now()
	atomic.AddInt64(&s.concurrentCount, 1)
	defer atomic.AddInt64(&s.concurrentCount, -1)
	
	metrics.IncActiveStreams("BidirectionalStream", "bidirectional")
	defer metrics.DecActiveStreams("BidirectionalStream", "bidirectional")

	sessionID := fmt.Sprintf("bidi_stream_%d", time.Now().UnixNano())
	
	// Use goroutines to handle concurrent sending and receiving
	var wg sync.WaitGroup
	errChan := make(chan error, 2)

	// Goroutine for receiving requests
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			req, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				errChan <- fmt.Errorf("receive error: %w", err)
				return
			}

			// Record each request to database
			record := &db.BenchmarkRecord{
				RequestType:        "BidirectionalStream",
				SequenceID:         &req.SequenceId,
				UserID:             req.UserId,
				SessionID:          sessionID,
				Payload:            req.Payload,
				Metadata:           req.Metadata,
				QueueTimeNs:        0,
				ConcurrentRequests: int32(atomic.LoadInt64(&s.concurrentCount)),
			}

			recordID, dbErr := s.recordRequestMetrics(stream.Context(), record)
			if dbErr != nil {
				log.Error().Err(dbErr).Str("record_id", recordID).Msg("Failed to record bidirectional stream request")
			}

			// Send immediate response
			response := &proto.StreamResponse{
				SequenceId: req.SequenceId,
				Result:     fmt.Sprintf("Processed: %s", req.Payload),
				Timestamp:  time.Now().UnixNano(),
				Stats: &proto.ProcessingStats{
					ProcessingTimeNs:   time.Since(start).Nanoseconds(),
					QueueTimeNs:        0,
					ConcurrentRequests: int32(atomic.LoadInt64(&s.concurrentCount)),
				},
				RecordId:        recordID,
				DatabaseSuccess: dbErr == nil,
				ApiResponse:     "BidirectionalStream response",
			}

			if sendErr := stream.Send(response); sendErr != nil {
				errChan <- fmt.Errorf("send error: %w", sendErr)
				return
			}

			priority := "NORMAL"
			if req.Type == proto.MessageType_HIGH_PRIORITY {
				priority = "HIGH_PRIORITY"
			} else if req.Type == proto.MessageType_BATCH {
				priority = "BATCH"
			}
			metrics.RecordMessageProcessed("BidirectionalStream", priority, len(req.Payload))
		}
	}()

	// Wait for completion or error
	wg.Wait()
	close(errChan)

	duration := time.Since(start)
	
	if err := <-errChan; err != nil {
		metrics.RecordStreamingRequest("BidirectionalStream", "bidirectional", "error", duration)
		log.Error().Err(err).Str("session_id", sessionID).Msg("Bidirectional stream error")
		return status.Errorf(codes.Internal, "bidirectional stream error: %v", err)
	}

	metrics.RecordStreamingRequest("BidirectionalStream", "bidirectional", "success", duration)
	
	log.Info().
		Str("session_id", sessionID).
		Dur("duration", duration).
		Msg("Bidirectional stream processed")

	return nil
}

// LargeDataStream implements large data streaming RPC
func (s *BenchmarkService) LargeDataStream(stream proto.StreamingBenchmarkService_LargeDataStreamServer) error {
	start := time.Now()
	atomic.AddInt64(&s.concurrentCount, 1)
	defer atomic.AddInt64(&s.concurrentCount, -1)
	
	metrics.IncActiveStreams("LargeDataStream", "client")
	defer metrics.DecActiveStreams("LargeDataStream", "client")

	sessionID := fmt.Sprintf("large_data_stream_%d", time.Now().UnixNano())
	var totalBytes int64
	var chunks []*proto.DataChunk
	var filename string

	// Receive all data chunks
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			duration := time.Since(start)
			metrics.RecordStreamingRequest("LargeDataStream", "client", "error", duration)
			log.Error().Err(err).Msg("Error receiving data chunk")
			return status.Errorf(codes.Internal, "chunk receive error: %v", err)
		}

		chunks = append(chunks, chunk)
		totalBytes += int64(len(chunk.Data))
		if filename == "" {
			filename = chunk.Filename
		}

		// Record chunk to database
		recordID := generateRecordID("LargeDataStream", chunk.ChunkId)
		dbChunk := &db.DataChunk{
			RecordID:    recordID,
			ChunkID:     chunk.ChunkId,
			Filename:    chunk.Filename,
			DataSize:    int32(len(chunk.Data)),
			TotalChunks: chunk.TotalChunks,
		}

		dbStart := time.Now()
		if err := s.db.InsertDataChunk(stream.Context(), dbChunk); err != nil {
			dbDuration := time.Since(dbStart)
			metrics.RecordDatabaseOperation("insert_data_chunk", "error", dbDuration)
			log.Error().Err(err).Str("record_id", recordID).Msg("Failed to insert data chunk")
		} else {
			dbDuration := time.Since(dbStart)
			metrics.RecordDatabaseOperation("insert_data_chunk", "success", dbDuration)
		}

		metrics.RecordMessageProcessed("LargeDataStream", "NORMAL", len(chunk.Data))
	}

	// Record overall file processing
	record := &db.BenchmarkRecord{
		RequestType:        "LargeDataStream",
		SequenceID:         &[]int32{int32(len(chunks))}[0],
		UserID:             "file_transfer_user",
		SessionID:          sessionID,
		Payload:            fmt.Sprintf("File: %s, Chunks: %d, Size: %d bytes", filename, len(chunks), totalBytes),
		Metadata:           map[string]string{"filename": filename, "total_chunks": fmt.Sprintf("%d", len(chunks))},
		QueueTimeNs:        0,
		ConcurrentRequests: int32(atomic.LoadInt64(&s.concurrentCount)),
	}

	recordID, err := s.recordRequestMetrics(stream.Context(), record)

	// Send response
	response := &proto.StreamResponse{
		SequenceId: int32(len(chunks)),
		Result:     fmt.Sprintf("Received file %s: %d chunks, %d total bytes", filename, len(chunks), totalBytes),
		Timestamp:  time.Now().UnixNano(),
		Stats: &proto.ProcessingStats{
			ProcessingTimeNs:   time.Since(start).Nanoseconds(),
			QueueTimeNs:        0,
			ConcurrentRequests: int32(atomic.LoadInt64(&s.concurrentCount)),
		},
		RecordId:        recordID,
		DatabaseSuccess: err == nil,
		ApiResponse:     "LargeDataStream completed successfully",
	}

	duration := time.Since(start)
	
	if err != nil {
		metrics.RecordStreamingRequest("LargeDataStream", "client", "error", duration)
		return status.Errorf(codes.Internal, "database error: %v", err)
	}

	metrics.RecordStreamingRequest("LargeDataStream", "client", "success", duration)
	
	log.Info().
		Str("record_id", recordID).
		Str("session_id", sessionID).
		Str("filename", filename).
		Int("chunk_count", len(chunks)).
		Int64("total_bytes", totalBytes).
		Dur("duration", duration).
		Msg("Large data stream processed")

	return stream.SendAndClose(response)
}
