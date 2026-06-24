//! gRPC adapter implementing the generated `Users` service: unary, server
//! streaming, client streaming, and bidirectional streaming.

use std::pin::Pin;
use std::sync::Arc;

use chrono::Utc;
use futures::{Stream, StreamExt};
use tonic::{Request, Response, Status, Streaming};

use crate::domain::{NewUser, User, UserError};
use crate::pb::users_server::{Users, UsersServer};
use crate::pb::{
    ChatMessage, CreateUserRequest, GetUserRequest, ImportSummary, ListUsersRequest, UserReply,
};
use crate::service::UserService;

/// Builds the tonic service wrapping the application service.
pub fn service(svc: Arc<UserService>) -> UsersServer<UsersGrpc> {
    UsersServer::new(UsersGrpc { svc })
}

pub struct UsersGrpc {
    svc: Arc<UserService>,
}

fn to_reply(u: User) -> UserReply {
    UserReply {
        id: u.id,
        email: u.email,
        full_name: u.full_name,
        created_at: u.created_at.to_rfc3339(),
    }
}

#[tonic::async_trait]
impl Users for UsersGrpc {
    // ----- Unary ----------------------------------------------------------

    async fn get_user(
        &self,
        request: Request<GetUserRequest>,
    ) -> Result<Response<UserReply>, Status> {
        let user = self.svc.get_by_id(request.into_inner().id).await?;
        Ok(Response::new(to_reply(user)))
    }

    async fn create_user(
        &self,
        request: Request<CreateUserRequest>,
    ) -> Result<Response<UserReply>, Status> {
        let req = request.into_inner();
        let input = NewUser::validate(req.email, req.full_name)?;
        let user = self.svc.create(input).await?;
        Ok(Response::new(to_reply(user)))
    }

    // ----- Server streaming ----------------------------------------------

    type ListUsersStream = Pin<Box<dyn Stream<Item = Result<UserReply, Status>> + Send>>;

    // `tonic::Status` is large by design; the streaming item type is fixed by
    // the generated trait, so the large-Err lint is not actionable here.
    #[allow(clippy::result_large_err)]
    async fn list_users(
        &self,
        request: Request<ListUsersRequest>,
    ) -> Result<Response<Self::ListUsersStream>, Status> {
        let prefix = match request.into_inner().email_prefix {
            p if p.is_empty() => None,
            p => Some(p),
        };
        // `Send` over HTTP/2 flow control, so a slow client back-pressures the
        // portal all the way to Postgres — end-to-end, no buffer explodes.
        let stream = self
            .svc
            .stream_all(prefix)
            .map(|res| res.map(to_reply).map_err(Status::from));
        Ok(Response::new(Box::pin(stream)))
    }

    // ----- Client streaming ----------------------------------------------

    async fn import_users(
        &self,
        request: Request<Streaming<CreateUserRequest>>,
    ) -> Result<Response<ImportSummary>, Status> {
        let mut stream = request.into_inner();
        let mut imported = 0i64;
        let mut skipped = 0i64;
        let mut errors = Vec::new();

        while let Some(req) = stream.message().await? {
            match NewUser::validate(req.email, req.full_name) {
                Err(UserError::Validation(msg)) => {
                    skipped += 1;
                    errors.push(msg);
                }
                Err(_) => skipped += 1,
                Ok(input) => match self.svc.create(input).await {
                    Ok(_) => imported += 1,
                    Err(UserError::DuplicateEmail(_)) => skipped += 1,
                    Err(e) => {
                        skipped += 1;
                        errors.push(e.to_string());
                    }
                },
            }
        }

        Ok(Response::new(ImportSummary { imported, skipped, errors }))
    }

    // ----- Bidirectional streaming ---------------------------------------

    type ChatStream = Pin<Box<dyn Stream<Item = Result<ChatMessage, Status>> + Send>>;

    async fn chat(
        &self,
        request: Request<Streaming<ChatMessage>>,
    ) -> Result<Response<Self::ChatStream>, Status> {
        let mut inbound = request.into_inner();
        let outbound = async_stream::try_stream! {
            while let Some(msg) = inbound.message().await? {
                yield ChatMessage {
                    from: "server".into(),
                    text: format!("echo: {}", msg.text),
                    ts_millis: Utc::now().timestamp_millis(),
                };
            }
        };
        Ok(Response::new(Box::pin(outbound)))
    }
}
