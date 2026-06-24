//! REST surface (Axum): CRUD over `/api/users`, an NDJSON streaming list,
//! health probes, and `/metrics`. Mirrors the Kotlin Routes / Go httpserver.

use std::sync::Arc;
use std::time::Instant;

use axum::body::{Body, Bytes};
use axum::extract::{Path, Query, Request, State};
use axum::http::{header, StatusCode};
use axum::middleware::{self, Next};
use axum::response::Response;
use axum::routing::{get, post};
use axum::Router;
use futures::StreamExt;
use serde::{Deserialize, Serialize};

use crate::domain::{NewUser, User, UserError};
use crate::observability::Metrics;
use crate::service::UserService;

#[derive(Clone)]
struct AppState {
    svc: Arc<UserService>,
    metrics: Arc<Metrics>,
}

/// Builds the routed, instrumented Axum router.
pub fn router(svc: Arc<UserService>, metrics: Arc<Metrics>) -> Router {
    let state = AppState { svc, metrics: metrics.clone() };

    Router::new()
        .route("/healthz", get(|| async { "ok" }))
        .route("/readyz", get(|| async { "ok" }))
        .route("/metrics", get(metrics_handler))
        .route("/api/users/:id", get(get_user))
        .route("/api/users", post(create_user).get(stream_users))
        .route("/api/users/bulk", post(bulk_create))
        .layer(middleware::from_fn_with_state(metrics, track_metrics))
        .with_state(state)
}

// ---- wire DTOs -----------------------------------------------------------

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct UserJson {
    id: i64,
    email: String,
    full_name: String,
    created_at: String,
}

impl From<User> for UserJson {
    fn from(u: User) -> Self {
        Self {
            id: u.id,
            email: u.email,
            full_name: u.full_name,
            created_at: u.created_at.to_rfc3339(),
        }
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct CreateBody {
    #[serde(default)]
    email: String,
    #[serde(default)]
    full_name: String,
}

#[derive(Deserialize)]
struct StreamQuery {
    #[serde(rename = "emailPrefix")]
    email_prefix: Option<String>,
}

// ---- handlers ------------------------------------------------------------

async fn get_user(State(st): State<AppState>, Path(id): Path<String>) -> Response {
    let Ok(id) = id.parse::<i64>() else {
        return problem(StatusCode::BAD_REQUEST, "Bad Request", "id must be an integer");
    };
    match st.svc.get_by_id(id).await {
        Ok(user) => json(StatusCode::OK, &UserJson::from(user)),
        Err(e @ UserError::NotFound(_)) => problem(StatusCode::NOT_FOUND, "Not Found", &e.to_string()),
        Err(e) => internal(&e),
    }
}

async fn create_user(State(st): State<AppState>, body: Bytes) -> Response {
    let Ok(req) = serde_json::from_slice::<CreateBody>(&body) else {
        return problem(StatusCode::BAD_REQUEST, "Bad Request", "json expected");
    };
    let input = match NewUser::validate(req.email, req.full_name) {
        Ok(input) => input,
        Err(e) => return problem(StatusCode::BAD_REQUEST, "Bad Request", &e.to_string()),
    };
    match st.svc.create(input).await {
        Ok(user) => json(StatusCode::CREATED, &UserJson::from(user)),
        Err(e @ UserError::DuplicateEmail(_)) => {
            problem(StatusCode::CONFLICT, "Conflict", &e.to_string())
        }
        Err(e) => internal(&e),
    }
}

async fn bulk_create(State(st): State<AppState>, body: Bytes) -> Response {
    let Ok(rows) = serde_json::from_slice::<Vec<CreateBody>>(&body) else {
        return problem(StatusCode::BAD_REQUEST, "Bad Request", "array expected");
    };
    let mut inputs = Vec::with_capacity(rows.len());
    for row in rows {
        match NewUser::validate(row.email, row.full_name) {
            Ok(input) => inputs.push(input),
            Err(e) => return problem(StatusCode::BAD_REQUEST, "Bad Request", &e.to_string()),
        }
    }
    match st.svc.bulk_create(&inputs).await {
        Ok(created) => {
            let out: Vec<UserJson> = created.into_iter().map(UserJson::from).collect();
            json(StatusCode::CREATED, &out)
        }
        Err(e) => internal(&e),
    }
}

/// Streams users as NDJSON (one JSON object per line). Each flush blocks when
/// the client is slow, back-pressuring the Postgres portal in `stream_all`.
async fn stream_users(State(st): State<AppState>, Query(q): Query<StreamQuery>) -> Response {
    let prefix = q.email_prefix.filter(|p| !p.is_empty());
    let stream = st.svc.stream_all(prefix).map(|res| match res {
        Ok(user) => {
            let mut line = serde_json::to_vec(&UserJson::from(user)).unwrap_or_default();
            line.push(b'\n');
            Ok::<Bytes, std::io::Error>(Bytes::from(line))
        }
        Err(e) => Err(std::io::Error::other(e.to_string())),
    });

    Response::builder()
        .status(StatusCode::OK)
        .header(header::CONTENT_TYPE, "application/x-ndjson")
        .body(Body::from_stream(stream))
        .expect("valid ndjson response")
}

async fn metrics_handler(State(st): State<AppState>) -> Response {
    let (body, content_type) = st.metrics.render();
    Response::builder()
        .status(StatusCode::OK)
        .header(header::CONTENT_TYPE, content_type)
        .body(Body::from(body))
        .expect("valid metrics response")
}

// ---- middleware ----------------------------------------------------------

/// Times each request and records latency by method + final status code.
async fn track_metrics(State(metrics): State<Arc<Metrics>>, req: Request, next: Next) -> Response {
    let method = req.method().clone();
    let started = Instant::now();
    let response = next.run(req).await;
    metrics.observe_http(
        method.as_str(),
        response.status().as_u16(),
        started.elapsed().as_secs_f64(),
    );
    response
}

// ---- helpers -------------------------------------------------------------

fn json<T: Serialize>(status: StatusCode, value: &T) -> Response {
    let body = serde_json::to_vec(value).unwrap_or_default();
    Response::builder()
        .status(status)
        .header(header::CONTENT_TYPE, "application/json")
        .body(Body::from(body))
        .expect("valid json response")
}

fn internal(e: &UserError) -> Response {
    problem(StatusCode::INTERNAL_SERVER_ERROR, "Internal Server Error", &e.to_string())
}

/// RFC 7807 problem+json response.
fn problem(status: StatusCode, title: &str, detail: &str) -> Response {
    let body = serde_json::json!({
        "type": "about:blank",
        "title": title,
        "status": status.as_u16(),
        "detail": detail,
    });
    Response::builder()
        .status(status)
        .header(header::CONTENT_TYPE, "application/problem+json")
        .body(Body::from(serde_json::to_vec(&body).unwrap_or_default()))
        .expect("valid problem response")
}
