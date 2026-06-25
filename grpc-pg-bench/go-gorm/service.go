package main

import (
	"context"
	"time"

	benchv1 "github.com/beam/grpc-pg-bench/gen/benchv1"
)

// commandService implements bench.v1.CommandService. Thin on purpose: compute
// the checksum, stamp receive time, delegate to the GORM Db, map the result
// back onto the proto response. All DB concerns live in db.go.
type commandService struct {
	benchv1.UnimplementedCommandServiceServer
	db *Db
}

// Execute: single autocommit INSERT (through GORM Create).
func (s *commandService) Execute(ctx context.Context, req *benchv1.CommandRequest) (*benchv1.CommandResponse, error) {
	recv := time.Now().UnixMicro()
	checksum := fnv1a(req.Payload)

	id, err := s.db.InsertCommand(ctx, req.WorkflowId, req.CommandType, req.Payload, req.Seq, int64(checksum))
	if err != nil {
		return nil, err
	}
	return &benchv1.CommandResponse{
		Id:               id,
		Checksum:         checksum,
		ReceivedAtMicros: recv,
	}, nil
}

// ExecuteTx: three statements in one transaction (command + state + outbox).
func (s *commandService) ExecuteTx(ctx context.Context, req *benchv1.CommandRequest) (*benchv1.CommandResponse, error) {
	recv := time.Now().UnixMicro()
	checksum := fnv1a(req.Payload)

	id, err := s.db.ExecuteTx(ctx, req.WorkflowId, req.CommandType, req.Payload, req.Seq, int64(checksum))
	if err != nil {
		return nil, err
	}
	return &benchv1.CommandResponse{
		Id:               id,
		Checksum:         checksum,
		ReceivedAtMicros: recv,
	}, nil
}

// GetState: single read by workflow_id.
func (s *commandService) GetState(ctx context.Context, req *benchv1.GetStateRequest) (*benchv1.StateResponse, error) {
	row, err := s.db.GetState(ctx, req.WorkflowId)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return &benchv1.StateResponse{Found: false, WorkflowId: req.WorkflowId}, nil
	}
	return &benchv1.StateResponse{
		Found:           true,
		WorkflowId:      row.WorkflowID,
		State:           row.State,
		Version:         row.Version,
		UpdatedAtMicros: row.UpdatedAtMicros,
	}, nil
}
