//! Prometheus registry, HTTP/gRPC request timing, and the `/metrics` exposition
//! — the Rust analogue of the Micrometer setup.

mod grpc_metrics;
mod metrics;

pub use grpc_metrics::GrpcMetricsLayer;
pub use metrics::Metrics;
