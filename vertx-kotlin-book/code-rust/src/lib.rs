//! users-rust — the Rust port of the Kotlin/Vert.x users service: the same
//! REST + gRPC surface, backed by tokio-postgres. This crate root wires the
//! modules together and exposes [`run`], the composition root.

pub mod config;
pub mod db;
pub mod domain;
pub mod observability;
pub mod service;
pub mod transport;

mod migrate;
mod pb;

use std::net::SocketAddr;
use std::sync::Arc;

use tokio_util::sync::CancellationToken;
use tonic::transport::Server;

use crate::config::Config;
use crate::db::Repository;
use crate::observability::Metrics;
use crate::service::UserService;

/// Boots both servers and blocks until a shutdown signal, then drains them.
pub async fn run() -> anyhow::Result<()> {
    let cfg = Config::load()?;
    tracing::info!(
        http_port = cfg.http.port,
        grpc_port = cfg.grpc.port,
        db = %format!("{}:{}/{}", cfg.db.host, cfg.db.port, cfg.db.database),
        "config loaded"
    );

    // ---- data layer ------------------------------------------------------
    let pool = db::new_pool(&cfg.db)?;
    if cfg.db.schema_on_startup {
        migrate::run(&pool).await?;
        tracing::info!("schema applied");
    }
    let repo = Repository::new(pool, db::conn_string(&cfg.db));
    let svc = Arc::new(UserService::new(repo.clone()));
    let metrics = Arc::new(Metrics::new());

    // ---- LISTEN/NOTIFY hook (logs ids inserted by any DB client) ---------
    match repo.listen_for_new_users().await {
        Ok(mut rx) => {
            tokio::spawn(async move {
                while let Some(id) = rx.recv().await {
                    tracing::debug!(user_id = id, "new user notified");
                }
            });
        }
        Err(e) => tracing::warn!(error = %e, "failed to start LISTEN/NOTIFY"),
    }

    // ---- shutdown coordination ------------------------------------------
    let shutdown = CancellationToken::new();
    {
        let shutdown = shutdown.clone();
        tokio::spawn(async move {
            shutdown_signal().await;
            tracing::info!("shutdown signal received");
            shutdown.cancel();
        });
    }

    // ---- gRPC server -----------------------------------------------------
    let grpc_addr: SocketAddr = format!("0.0.0.0:{}", cfg.grpc.port).parse()?;
    let reflection = tonic_reflection::server::Builder::configure()
        .register_encoded_file_descriptor_set(pb::FILE_DESCRIPTOR_SET)
        .build_v1()?;
    let grpc = {
        let svc = svc.clone();
        let metrics = metrics.clone();
        let shutdown = shutdown.clone();
        tokio::spawn(async move {
            tracing::info!(addr = %grpc_addr, "gRPC server listening");
            Server::builder()
                .layer(metrics.grpc_layer())
                .add_service(reflection)
                .add_service(transport::grpc::service(svc))
                .serve_with_shutdown(grpc_addr, shutdown.cancelled_owned())
                .await
                .map_err(anyhow::Error::from)
        })
    };

    // ---- HTTP server -----------------------------------------------------
    let http_addr = format!("{}:{}", cfg.http.host, cfg.http.port);
    let http = {
        let app = transport::http::router(svc, metrics);
        let shutdown = shutdown.clone();
        tokio::spawn(async move {
            let listener = tokio::net::TcpListener::bind(&http_addr).await?;
            tracing::info!(addr = %http_addr, "HTTP server listening");
            axum::serve(listener, app)
                .with_graceful_shutdown(shutdown.cancelled_owned())
                .await
                .map_err(anyhow::Error::from)
        })
    };

    let (grpc_res, http_res) = tokio::try_join!(grpc, http)?;
    grpc_res?;
    http_res?;
    tracing::info!("shutdown complete");
    Ok(())
}

/// Resolves on SIGINT (Ctrl-C) or SIGTERM.
async fn shutdown_signal() {
    let ctrl_c = async {
        let _ = tokio::signal::ctrl_c().await;
    };

    #[cfg(unix)]
    let terminate = async {
        if let Ok(mut sig) =
            tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
        {
            sig.recv().await;
        }
    };
    #[cfg(not(unix))]
    let terminate = std::future::pending::<()>();

    tokio::select! {
        _ = ctrl_c => {}
        _ = terminate => {}
    }
}
