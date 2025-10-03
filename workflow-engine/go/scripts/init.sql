-- Create the waves schema
CREATE SCHEMA IF NOT EXISTS waves;

-- Create sequences
CREATE SEQUENCE IF NOT EXISTS waves.endpoint_id_seq;
CREATE SEQUENCE IF NOT EXISTS waves.waves_workflow_id_seq;
CREATE SEQUENCE IF NOT EXISTS waves.waves_state_id_seq;
CREATE SEQUENCE IF NOT EXISTS waves.waves_variables_id_seq;

-- Create endpoint table
CREATE TABLE IF NOT EXISTS "waves"."endpoint" (
"id" int8 NOT NULL DEFAULT nextval('waves.endpoint_id_seq'::regclass),
"name" text NOT NULL,
"endpoint" text NOT NULL,
"version" int4 NOT NULL DEFAULT 0,
"created_at" timestamp,
"updated_at" timestamp,
PRIMARY KEY ("id")
);

-- Create unique index on endpoint name
CREATE UNIQUE INDEX IF NOT EXISTS waves_endpoint_workflow_name_idx ON waves.endpoint (name);

-- Create workflow table
CREATE TABLE IF NOT EXISTS "waves"."workflow" (
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

-- Create state table
CREATE TABLE IF NOT EXISTS "waves"."state" (
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

-- Create index on workflow_id for state table
CREATE INDEX IF NOT EXISTS waves_state_workflow_id_idx ON waves.state USING btree (workflow_id);

-- Create variables table
CREATE TABLE IF NOT EXISTS "waves"."variables" (
"id" int8 NOT NULL DEFAULT nextval('waves.waves_variables_id_seq'::regclass),
"workflow_id" int8 NOT NULL DEFAULT 0,
"last_task_name" text NOT NULL,
"data" JSONB NOT NULL,
"version" int4 NOT NULL DEFAULT 0,
"created_at" timestamp,
"updated_at" timestamp,
PRIMARY KEY ("id")
);

-- Create index on workflow_id for variables table
CREATE INDEX IF NOT EXISTS waves_variables_workflow_id_idx ON waves.variables USING btree (workflow_id);