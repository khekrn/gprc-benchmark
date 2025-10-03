## Introduction

We want to play with GRPC bi-directional streaming and test the performance of Vertx and golang. We are gonna implement 3 variants

- Go based bidirectional streaming implementation server
- Vertx with Kotlin Coroutine GRPC bidirectional streaming server
- Vertx with Java async GRPC bidirectional streaming server

## What we need to build ?

We are going to build a mini workflow engine, where engine and worker's are decoupled and communicate via grpc bidirectional streaming. Worker registers the workflow with api-endpoint(grpc endpoint) to trigger and start the workflow. When we need to start the workflow, we need to hit an API to the workflow engine server which validates and creates the entry in `workflow` table and creates or uses the bidirectional streaming connection between server and worker and triggers the worker endpoint. Worker run's the workflow by executing set of functions defined workflow as code, As it run's each step it also sends the progress to the server via streaming.

We define the workflow in worker as ordered set of functions and we currently support task and condition.

## Postgresql Schema:

### Endpoint Table:

```sql
CREATE SEQUENCE IF NOT EXISTS waves.endpoint_id_seq;

-- Table Definition
CREATE TABLE "waves"."endpoint" (
"id" int8 NOT NULL DEFAULT nextval('waves.endpoint_id_seq'::regclass),
"name" text NOT NULL,
"endpoint" text NOT NULL,
"version" int4 NOT NULL DEFAULT 0,
"created_at" timestamp,
"updated_at" timestamp,
PRIMARY KEY ("id")
);

- Indices
  CREATE UNIQUE INDEX waves_endpoint_workflow_name_idx ON endpoint.state (name);
```

### Workflow Table:

```sql
CREATE SEQUENCE IF NOT EXISTS waves.waves_workflow_id_seq;

-- Table Definition
CREATE TABLE "waves"."workflow" (
"id" int8 NOT NULL DEFAULT nextval('waves.waves_workflow_id_seq'::regclass),
"name" text NOT NULL,
"rid" text NOT NULL,
"type" varchar(255) NOT NULL,
"status" varchar(4) NOT NULL,
"version" int4 NOT NULL DEFAULT 0,
"created_at" timestamp,
"updated_at" timestamp,
PRIMARY KEY ("id")
);
```

### State Table:

```sql
-- Sequence and defined type
CREATE SEQUENCE IF NOT EXISTS waves.waves_state_id_seq;

-- Table Definition
CREATE TABLE "waves"."state" (
"id" int8 NOT NULL DEFAULT nextval('waves.waves_state_id_seq'::regclass),
"workflow_id" int8 NOT NULL,
"name" text NOT NULL,
"type" varchar(255) NOT NULL,
"status" varchar(4) NOT NULL,
"version" int4 NOT NULL DEFAULT 0,
"created_at" timestamp,
"updated_at" timestamp,
PRIMARY KEY ("id")
);

-- Indices
CREATE INDEX waves_state_workflow_id_idx ON waves.state USING btree (workflow_id);
```

### Variables Table:

```sql

-- Sequence and defined type
CREATE SEQUENCE IF NOT EXISTS waves.waves_variables_id_seq;

-- Table Definition
CREATE TABLE "waves"."variables" (
"id" int8 NOT NULL DEFAULT nextval('waves.waves_variables_id_seq'::regclass),
"workflow_id" int8 NOT NULL DEFAULT 0,
"last_task_name" text NOT NULL,
"data" JSONB NOT NULL,
"version" int4 NOT NULL DEFAULT 0,
"created_at" timestamp,
"updated_at" timestamp,
PRIMARY KEY ("id")
);

-- Indices
CREATE INDEX waves_variables_workflow_id_idx ON waves.variables USING btree (workflow_id);

```

## Workflow: Loan Approval

PostLoanApplication -> If Success -> PanVerification -> If Success -> AadhaarVerification -> If Success -> BureauPull -> If Success -> FinalDecision -> If Success -> UpdateStatus -> If Success -> SendCallback
-> If Failed -> SendCallback -> If Failed -> SendCallback -> If Failed -> SendCallback -> If Failed -> SendCallback -> If Failed -> SendCallback -> If Failed -> SendCallback

## Sequence:

- Worker Registers the workflow and an endpoint(GRPC) to the server via UNARY RPC call.

  - Server creates an entry in endpoint table
  - Returns the response to worker

- Post an REST API to start a workflow on server

  - Server validates the corresponding workflow name exist and creates an entry in the workflow table.
  - Server return's the proper HTTP status back to the client
  - Server establishes or reuses bidirectional streaming connection to the worker and delegates the payload we received triggers the registered endpoint which is configured(Async)

- Workers executes the workflow

  - On before running any state it creates an entry in database under state table with p status by sending an message to server
  - executes the function
  - Any variable set/update/delete everything is in-memory
  - on execute comepletion, again it send the message to server state is executed with status s or f
  - The above step's(1-3) continue for all function execution and on final state we update the final status of the workflow and saves the variables as json in variables table.
    State table example how it looks like - PostApplication = s - PostApplicationCond = s - PanVerification = s - PanVerificationCond = S

- From the worker as well as server we don't close the connection(bidirectional streaming connection) immediately after workflow is complete we will keep it open only after idle for more than 300 seconds we close the connection.

For the client implementation, Let's use only the golang based implementation
