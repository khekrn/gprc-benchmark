// Package observability holds the Prometheus registry, HTTP/gRPC timing, and
// the /metrics handler — the Go analogue of the Micrometer setup.
package observability

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

type Metrics struct {
	Registry     *prometheus.Registry
	httpDuration *prometheus.HistogramVec
	grpcDuration *prometheus.HistogramVec
}

func New() *Metrics {
	reg := prometheus.NewRegistry()
	// Runtime + process metrics (the analogue of jvmMetricsEnabled).
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	httpDur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_seconds",
		Help:    "HTTP request latency.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "status"})
	grpcDur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "grpc_request_seconds",
		Help:    "gRPC request latency.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "code"})
	reg.MustRegister(httpDur, grpcDur)
	return &Metrics{Registry: reg, httpDuration: httpDur, grpcDuration: grpcDur}
}

// Handler serves the Prometheus exposition format at /metrics. It is a plain
// net/http handler, mounted into Fiber via the adaptor middleware.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}

// ObserveHTTP records one HTTP request's latency by method and status code.
// Called from the Fiber timing middleware.
func (m *Metrics) ObserveHTTP(method string, status int, seconds float64) {
	m.httpDuration.WithLabelValues(method, strconv.Itoa(status)).Observe(seconds)
}

// UnaryServerInterceptor times unary gRPC calls by method and status code.
func (m *Metrics) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		started := time.Now()
		resp, err := handler(ctx, req)
		m.grpcDuration.WithLabelValues(info.FullMethod, status.Code(err).String()).
			Observe(time.Since(started).Seconds())
		return resp, err
	}
}

// StreamServerInterceptor times streaming gRPC calls by method and status code.
func (m *Metrics) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		started := time.Now()
		err := handler(srv, ss)
		m.grpcDuration.WithLabelValues(info.FullMethod, status.Code(err).String()).
			Observe(time.Since(started).Seconds())
		return err
	}
}
