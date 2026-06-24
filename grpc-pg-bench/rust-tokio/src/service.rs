//! The `bench.v1.CommandService` gRPC implementation. This layer is thin on
//! purpose: compute the checksum, stamp the receive time, delegate to `Db`,
//! and map the result back onto the proto response. All database concerns live
//! in `db.rs`; all wire concerns are these three methods.

use std::time::{SystemTime, UNIX_EPOCH};

use tonic::{Request, Response, Status};

use crate::db::Db;
use crate::fnv::fnv1a_32;
use crate::proto::bench_v1::command_service_server::CommandService;
use crate::proto::bench_v1::{CommandRequest, CommandResponse, GetStateRequest, StateResponse};

pub struct CommandSvc {
    db: Db,
}

impl CommandSvc {
    pub fn new(db: Db) -> Self {
        Self { db }
    }
}

#[tonic::async_trait]
impl CommandService for CommandSvc {
    /// Single autocommit INSERT.
    async fn execute(
        &self,
        req: Request<CommandRequest>,
    ) -> Result<Response<CommandResponse>, Status> {
        let recv = now_micros();
        let r = req.into_inner();
        let checksum = fnv1a_32(&r.payload);

        let id = self
            .db
            .insert_command(&r.workflow_id, &r.command_type, &r.payload, r.seq, checksum as i64)
            .await?;

        Ok(Response::new(CommandResponse {
            id,
            checksum,
            received_at_micros: recv,
        }))
    }

    /// Three statements in one transaction (command + state + outbox).
    async fn execute_tx(
        &self,
        req: Request<CommandRequest>,
    ) -> Result<Response<CommandResponse>, Status> {
        let recv = now_micros();
        let r = req.into_inner();
        let checksum = fnv1a_32(&r.payload);

        let id = self
            .db
            .execute_tx(&r.workflow_id, &r.command_type, &r.payload, r.seq, checksum as i64)
            .await?;

        Ok(Response::new(CommandResponse {
            id,
            checksum,
            received_at_micros: recv,
        }))
    }

    /// Single read by workflow_id.
    async fn get_state(
        &self,
        req: Request<GetStateRequest>,
    ) -> Result<Response<StateResponse>, Status> {
        let workflow_id = req.into_inner().workflow_id;

        let resp = match self.db.get_state(&workflow_id).await? {
            Some(s) => StateResponse {
                found: true,
                workflow_id,
                state: s.state,
                version: s.version,
                updated_at_micros: s.updated_at_micros,
            },
            None => StateResponse {
                found: false,
                workflow_id,
                state: String::new(),
                version: 0,
                updated_at_micros: 0,
            },
        };
        Ok(Response::new(resp))
    }
}

/// Server receive timestamp in unix micros. Saturates to 0 if the clock is
/// somehow before the epoch — same defensive default as the Go stack.
#[inline]
fn now_micros() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_micros() as i64)
        .unwrap_or(0)
}
