//! gRPC transport: wires the service + health reporter into tonic's native
//! HTTP/2 server, tuned to match the Go server's knobs, and serves until a
//! shutdown signal drains in-flight RPCs.
//!
//! We use tonic's built-in `transport::Server` rather than a hand-rolled
//! hyper/axum accept loop: tonic is already HTTP/2-only (no version sniffing
//! to pay for), and it exposes the exact flow-control / keepalive / nodelay
//! settings the Go server tunes — so this is both simpler and on equal footing.

use std::time::Duration;

use tonic::transport::Server;
use tracing::info;

use crate::config::Config;
use crate::db::Db;
use crate::proto::bench_v1::command_service_server::CommandServiceServer;
use crate::service::CommandSvc;

pub async fn serve(cfg: &Config, db: Db) -> Result<(), Box<dyn std::error::Error>> {
    let svc = CommandServiceServer::new(CommandSvc::new(db));

    // Standard gRPC health service — wired up for parity with what we'd ship,
    // not used by the loadgen.
    let (mut health_reporter, health_service) = tonic_health::server::health_reporter();
    health_reporter
        .set_serving::<CommandServiceServer<CommandSvc>>()
        .await;

    let addr = cfg.listen_addr.parse()?;
    info!(
        addr = %cfg.listen_addr,
        pool_min = cfg.pool_min,
        pool_max = cfg.pool_max,
        workers = cfg.worker_threads,
        "rust-tokio server listening"
    );

    Server::builder()
        // 1 MiB HTTP/2 windows, same as the Go server: the 64 KiB defaults make
        // the client wait for WINDOW_UPDATE frames between batches of short
        // RPCs, serializing throughput at high concurrency.
        .initial_stream_window_size(Some(1 << 20))
        .initial_connection_window_size(Some(1 << 20))
        // gRPC writes are tiny — disable Nagle so they aren't delayed.
        .tcp_nodelay(true)
        // Server-side keepalive, same intent as the Go ServerParameters: probe
        // idle connections so half-open TCP doesn't tie up a slot for a run.
        .http2_keepalive_interval(Some(Duration::from_secs(30)))
        .http2_keepalive_timeout(Some(Duration::from_secs(10)))
        .add_service(health_service)
        .add_service(svc)
        .serve_with_shutdown(addr, shutdown_signal())
        .await?;

    info!("graceful stop complete");
    Ok(())
}

/// Resolves on SIGTERM or SIGINT. tonic's `serve_with_shutdown` then stops
/// accepting new connections and lets in-flight RPCs finish before returning.
async fn shutdown_signal() {
    use tokio::signal::unix::{SignalKind, signal};
    let mut term = signal(SignalKind::terminate()).expect("install SIGTERM handler");
    let mut int = signal(SignalKind::interrupt()).expect("install SIGINT handler");
    tokio::select! {
        _ = term.recv() => {}
        _ = int.recv() => {}
    }
    info!("shutdown signal received, draining in-flight RPCs");
}
