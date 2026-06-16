package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// Request duration histogram
	RequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "grpc_request_duration_seconds",
			Help:    "Duration of gRPC requests",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "status", "server_type"},
	)

	// Request counter
	RequestTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "grpc_requests_total",
			Help: "Total number of gRPC requests",
		},
		[]string{"method", "status", "server_type"},
	)

	// Database operation duration
	DatabaseDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "database_operation_duration_seconds",
			Help:    "Duration of database operations",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation", "server_type"},
	)

	// Database operation counter
	DatabaseTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "database_operations_total",
			Help: "Total number of database operations",
		},
		[]string{"operation", "status", "server_type"},
	)

	// Message payload size
	MessagePayloadSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "message_payload_size_bytes",
			Help:    "Size of message payload in bytes",
			Buckets: []float64{1024, 2048, 4096, 8192, 16384, 32768, 65536},
		},
		[]string{"operation_type", "server_type"},
	)

	// Active connections
	ActiveConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "grpc_active_connections",
			Help: "Number of active gRPC connections",
		},
		[]string{"server_type"},
	)

	// Processing latency
	ProcessingLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "processing_latency_seconds",
			Help:    "Processing latency in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
		},
		[]string{"operation_type", "server_type"},
	)
)

func init() {
	// Register all metrics with Prometheus
	prometheus.MustRegister(
		RequestDuration,
		RequestTotal,
		DatabaseDuration,
		DatabaseTotal,
		MessagePayloadSize,
		ActiveConnections,
		ProcessingLatency,
	)
}

// MetricsServer starts a HTTP server for Prometheus metrics
func StartMetricsServer(port string) {
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		if err := http.ListenAndServe(":"+port, nil); err != nil {
			panic(err)
		}
	}()
}

// Timer is a helper for measuring duration
type Timer struct {
	start  time.Time
	labels prometheus.Labels
}

func NewTimer(labels prometheus.Labels) *Timer {
	return &Timer{
		start:  time.Now(),
		labels: labels,
	}
}

func (t *Timer) ObserveDuration(histogram *prometheus.HistogramVec) {
	duration := time.Since(t.start).Seconds()
	histogram.With(t.labels).Observe(duration)
}

func (t *Timer) ObserveProcessingLatency(operationType, serverType string) {
	duration := time.Since(t.start).Seconds()
	ProcessingLatency.WithLabelValues(operationType, serverType).Observe(duration)
}

// RecordRequest records a gRPC request
func RecordRequest(method, status, serverType string, duration time.Duration) {
	RequestTotal.WithLabelValues(method, status, serverType).Inc()
	RequestDuration.WithLabelValues(method, status, serverType).Observe(duration.Seconds())
}

// RecordDatabaseOperation records a database operation
func RecordDatabaseOperation(operation, status, serverType string, duration time.Duration) {
	DatabaseTotal.WithLabelValues(operation, status, serverType).Inc()
	DatabaseDuration.WithLabelValues(operation, serverType).Observe(duration.Seconds())
}

// RecordMessageSize records message payload size
func RecordMessageSize(operationType, serverType string, size int) {
	MessagePayloadSize.WithLabelValues(operationType, serverType).Observe(float64(size))
}

// IncrementActiveConnections increments active connections
func IncrementActiveConnections(serverType string) {
	ActiveConnections.WithLabelValues(serverType).Inc()
}

// DecrementActiveConnections decrements active connections
func DecrementActiveConnections(serverType string) {
	ActiveConnections.WithLabelValues(serverType).Dec()
}
