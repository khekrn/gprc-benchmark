# Multi-Instance Setup Guide

This guide explains how to run 2 servers and 3 workers for load balancing and high availability testing.

## Architecture

```
Redis (Discovery)
    |
    ├── Server 1 (HTTP: 8080, gRPC: 9090)
    └── Server 2 (HTTP: 8081, gRPC: 9091)
            |
            ├── Worker 1 (port: 9191)
            ├── Worker 2 (port: 9192)
            └── Worker 3 (port: 9193)
                    |
                PostgreSQL (Shared Database)
```

## Prerequisites

1. **Database**: PostgreSQL running on localhost:5432
2. **Redis**: Redis running on localhost:6379
3. **Build Tools**: Go and Make installed

## Setup Steps

### 1. Start the Servers (2 instances)

```bash
# Start both servers
./start-multiple-servers.sh
```

This will start:

- **Server 1**: HTTP on port 8080, gRPC on port 9090
- **Server 2**: HTTP on port 8081, gRPC on port 9091

### 2. Start the Workers (3 instances)

```bash
# Start all workers
./start-multiple-workers.sh
```

This will start:

- **Worker 1**: Port 9191 (loan_approval_worker)
- **Worker 2**: Port 9192 (loan_approval_worker_2)
- **Worker 3**: Port 9193 (loan_approval_worker_3)

### 3. Test Load Balancing

```bash
# Test both servers with workflows
./test-load-balancing.sh
```

## Monitoring

### Health Checks

- Server 1: http://localhost:8080/health
- Server 2: http://localhost:8081/health

### Log Files

- Server logs: `go/logs/server1.log`, `go/logs/server2.log`
- Worker logs: `worker/logs/worker1.log`, `worker/logs/worker2.log`, `worker/logs/worker3.log`

### Database Monitoring

```sql
-- Check endpoints registered
SELECT * FROM waves.endpoint;

-- Check running workflows
SELECT * FROM waves.workflow ORDER BY created_at DESC;

-- Check workflow states
SELECT * FROM waves.state ORDER BY created_at DESC;

-- Check workflow variables
SELECT * FROM waves.variables ORDER BY created_at DESC;
```

## Testing Scenarios

### 1. Basic Load Balancing

Send requests to both servers and verify workers handle them:

```bash
# Test Server 1
curl -X POST http://localhost:8080/api/v1/workflows/start \
  -H "Content-Type: application/json" \
  -d '{"workflow_name": "loan_approval", "payload": {"customer_id": "test1"}}'

# Test Server 2
curl -X POST http://localhost:8081/api/v1/workflows/start \
  -H "Content-Type: application/json" \
  -d '{"workflow_name": "loan_approval", "payload": {"customer_id": "test2"}}'
```

### 2. High Availability Test

Stop one server and verify the other continues working:

```bash
# Kill server 1
pkill -f "workflow-engine.*config.yaml"

# Test server 2 still works
curl http://localhost:8081/health
```

### 3. Worker Failure Recovery

Stop one worker and verify others continue:

```bash
# Kill worker 1
pkill -f "workflow-worker.*config.yaml"

# Workers 2 and 3 should still handle requests
```

## Cleanup

```bash
# Stop all servers
pkill -f workflow-engine

# Stop all workers
pkill -f workflow-worker
```

## Configuration Files

- **Servers**:

  - `go/config/config.yaml` (Server 1)
  - `go/config/config-server2.yaml` (Server 2)

- **Workers**:
  - `worker/config/config.yaml` (Worker 1)
  - `worker/config/config-worker2.yaml` (Worker 2)
  - `worker/config/config-worker3.yaml` (Worker 3)

## Expected Behavior

1. **Worker Registration**: All 3 workers should register with both servers
2. **Load Distribution**: Workflows should be distributed across available workers
3. **Database Persistence**: All workflow states and variables saved to database
4. **High Availability**: System continues if one server/worker fails
5. **Real-time Updates**: State changes visible immediately in database
