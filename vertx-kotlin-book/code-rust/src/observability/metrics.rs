//! The Prometheus registry and request-latency histograms.

use prometheus::{Encoder, HistogramVec, Registry, TextEncoder};

use super::GrpcMetricsLayer;

/// Holds the registry and the two latency histograms. Cheap to clone (every
/// field is `Arc`-backed), so it is shared by value across the HTTP middleware,
/// the gRPC tower layer, and the `/metrics` handler.
#[derive(Clone)]
pub struct Metrics {
    registry: Registry,
    http_seconds: HistogramVec,
    grpc_seconds: HistogramVec,
}

impl Metrics {
    pub fn new() -> Self {
        let registry = Registry::new();

        // Process metrics (the analogue of jvmMetricsEnabled / Go collectors).
        #[cfg(target_os = "linux")]
        {
            let process = prometheus::process_collector::ProcessCollector::for_self();
            let _ = registry.register(Box::new(process));
        }

        let http_seconds = HistogramVec::new(
            prometheus::HistogramOpts::new("http_request_seconds", "HTTP request latency."),
            &["method", "status"],
        )
        .expect("valid http histogram");
        let grpc_seconds = HistogramVec::new(
            prometheus::HistogramOpts::new("grpc_request_seconds", "gRPC request latency."),
            &["method", "code"],
        )
        .expect("valid grpc histogram");

        registry
            .register(Box::new(http_seconds.clone()))
            .expect("register http histogram");
        registry
            .register(Box::new(grpc_seconds.clone()))
            .expect("register grpc histogram");

        Self { registry, http_seconds, grpc_seconds }
    }

    /// Records one HTTP request's latency by method and status code.
    pub fn observe_http(&self, method: &str, status: u16, seconds: f64) {
        self.http_seconds
            .with_label_values(&[method, &status.to_string()])
            .observe(seconds);
    }

    /// Records one gRPC call's latency by full method and grpc-status code.
    pub(super) fn observe_grpc(&self, method: &str, code: &str, seconds: f64) {
        self.grpc_seconds
            .with_label_values(&[method, code])
            .observe(seconds);
    }

    /// A tower layer that times gRPC calls and feeds this registry.
    pub fn grpc_layer(&self) -> GrpcMetricsLayer {
        GrpcMetricsLayer::new(self.clone())
    }

    /// Renders the Prometheus exposition format for the `/metrics` endpoint.
    pub fn render(&self) -> (Vec<u8>, &'static str) {
        let mut buf = Vec::new();
        let encoder = TextEncoder::new();
        let _ = encoder.encode(&self.registry.gather(), &mut buf);
        (buf, "text/plain; version=0.0.4")
    }
}

impl Default for Metrics {
    fn default() -> Self {
        Self::new()
    }
}
