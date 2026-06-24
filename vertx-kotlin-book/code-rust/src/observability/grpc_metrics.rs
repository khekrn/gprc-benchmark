//! A tower [`Layer`] that times gRPC calls. It wraps the response body so the
//! observation fires when the stream actually ends (covering streaming RPCs,
//! not just time-to-first-byte) and reads the final `grpc-status` from the
//! response trailers — the Rust analogue of the Go timing interceptors.

use std::future::Future;
use std::pin::Pin;
use std::task::{ready, Context, Poll};
use std::time::Instant;

use http::{Request, Response};
use http_body::{Body, Frame, SizeHint};
use pin_project_lite::pin_project;
use tower::{Layer, Service};

use super::Metrics;

#[derive(Clone)]
pub struct GrpcMetricsLayer {
    metrics: Metrics,
}

impl GrpcMetricsLayer {
    pub(super) fn new(metrics: Metrics) -> Self {
        Self { metrics }
    }
}

impl<S> Layer<S> for GrpcMetricsLayer {
    type Service = GrpcMetrics<S>;

    fn layer(&self, inner: S) -> Self::Service {
        GrpcMetrics { inner, metrics: self.metrics.clone() }
    }
}

#[derive(Clone)]
pub struct GrpcMetrics<S> {
    inner: S,
    metrics: Metrics,
}

impl<S, ReqBody, ResBody> Service<Request<ReqBody>> for GrpcMetrics<S>
where
    S: Service<Request<ReqBody>, Response = Response<ResBody>>,
    ResBody: Body,
{
    type Response = Response<MetricsBody<ResBody>>;
    type Error = S::Error;
    type Future = MetricsFuture<S::Future>;

    fn poll_ready(&mut self, cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        self.inner.poll_ready(cx)
    }

    fn call(&mut self, req: Request<ReqBody>) -> Self::Future {
        MetricsFuture {
            method: req.uri().path().to_string(),
            start: Instant::now(),
            metrics: self.metrics.clone(),
            inner: self.inner.call(req),
        }
    }
}

pin_project! {
    pub struct MetricsFuture<F> {
        method: String,
        start: Instant,
        metrics: Metrics,
        #[pin]
        inner: F,
    }
}

impl<F, ResBody, E> Future for MetricsFuture<F>
where
    F: Future<Output = Result<Response<ResBody>, E>>,
    ResBody: Body,
{
    type Output = Result<Response<MetricsBody<ResBody>>, E>;

    fn poll(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<Self::Output> {
        let this = self.project();
        let response = ready!(this.inner.poll(cx))?;
        let (parts, body) = response.into_parts();
        let body = MetricsBody {
            inner: body,
            metrics: this.metrics.clone(),
            method: std::mem::take(this.method),
            start: *this.start,
            recorded: false,
        };
        Poll::Ready(Ok(Response::from_parts(parts, body)))
    }
}

pin_project! {
    /// Wraps a response body and records the request latency once the body is
    /// fully drained, labelling by the gRPC method and the `grpc-status` code.
    pub struct MetricsBody<B> {
        #[pin]
        inner: B,
        metrics: Metrics,
        method: String,
        start: Instant,
        recorded: bool,
    }
}

impl<B: Body> Body for MetricsBody<B> {
    type Data = B::Data;
    type Error = B::Error;

    fn poll_frame(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<Option<Result<Frame<Self::Data>, Self::Error>>> {
        let this = self.project();
        let polled = this.inner.poll_frame(cx);

        if let Poll::Ready(frame) = &polled {
            // gRPC carries the status in trailers; the stream ends either with a
            // trailers frame or by returning `None`. Record on whichever first.
            let code = match frame {
                Some(Ok(f)) => f
                    .trailers_ref()
                    .and_then(|t| t.get("grpc-status"))
                    .and_then(|v| v.to_str().ok())
                    .map(str::to_owned),
                Some(Err(_)) => Some("unknown".to_owned()),
                None => Some("0".to_owned()), // ended without trailers => OK
            };
            if let Some(code) = code {
                if !*this.recorded {
                    *this.recorded = true;
                    this.metrics
                        .observe_grpc(this.method, &code, this.start.elapsed().as_secs_f64());
                }
            }
        }
        polled
    }

    fn is_end_stream(&self) -> bool {
        self.inner.is_end_stream()
    }

    fn size_hint(&self) -> SizeHint {
        self.inner.size_hint()
    }
}
