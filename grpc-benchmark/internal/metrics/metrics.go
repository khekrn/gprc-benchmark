package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
)

var (
	// Request counters
	UnaryRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "grpc_unary_requests_total",
			Help: "Total number of unary gRPC requests",
		},
		[]string{"method", "status"},
	)

	StreamingRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "grpc_streaming_requests_total",
			Help: "Total number of streaming gRPC requests",
		},
		[]string{"method", "type", "status"},
	)

	// Request duration histograms
	UnaryRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "grpc_unary_request_duration_seconds",
			Help:    "Duration of unary gRPC requests",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
	)

	StreamingRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "grpc_streaming_request_duration_seconds",
			Help:    "Duration of streaming gRPC requests",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "type"},
	)

	// Database operation metrics
	DatabaseOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "database_operations_total",
			Help: "Total number of database operations",
		},
		[]string{"operation", "status"},
	)

	DatabaseOperationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "database_operation_duration_seconds",
			Help:    "Duration of database operations",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation"},
	)

	// Active connections and streams
	ActiveStreams = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "grpc_active_streams",
			Help: "Number of active gRPC streams",
		},
		[]string{"method", "type"},
	)

	DatabaseConnections = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "database_connections_active",
			Help: "Number of active database connections",
		},
	)

	// Message processing metrics
	MessagesProcessed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "messages_processed_total",
			Help: "Total number of messages processed",
		},
		[]string{"message_type", "priority"},
	)

	MessageSizeBytes = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "message_size_bytes",
			Help:    "Size of processed messages in bytes",
			Buckets: []float64{100, 1000, 10000, 100000, 1000000, 10000000},
		},
		[]string{"message_type"},
	)

	// Queue time metrics
	QueueTimeSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "queue_time_seconds",
			Help:    "Time spent in queue before processing",
			Buckets: []float64{0.001, 0.01, 0.1, 1, 10},
		},
		[]string{"method"},
	)

	// Custom benchmark metrics
	ConcurrentRequestsGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "concurrent_requests",
			Help: "Current number of concurrent requests being processed",
		},
	)
)

type Metrics struct {
	handler http.Handler
}

func NewMetrics() *Metrics {
	return &Metrics{
		handler: promhttp.Handler(),
	}
}

func (m *Metrics) Handler() http.Handler {
	return m.handler
}

// Helper functions to record metrics
func RecordUnaryRequest(method, status string, duration time.Duration) {
	UnaryRequestsTotal.WithLabelValues(method, status).Inc()
	UnaryRequestDuration.WithLabelValues(method).Observe(duration.Seconds())
}

func RecordStreamingRequest(method, streamType, status string, duration time.Duration) {
	StreamingRequestsTotal.WithLabelValues(method, streamType, status).Inc()
	StreamingRequestDuration.WithLabelValues(method, streamType).Observe(duration.Seconds())
}

func RecordDatabaseOperation(operation, status string, duration time.Duration) {
	DatabaseOperationsTotal.WithLabelValues(operation, status).Inc()
	DatabaseOperationDuration.WithLabelValues(operation).Observe(duration.Seconds())
}

func RecordMessageProcessed(messageType, priority string, sizeBytes int) {
	MessagesProcessed.WithLabelValues(messageType, priority).Inc()
	MessageSizeBytes.WithLabelValues(messageType).Observe(float64(sizeBytes))
}

func IncActiveStreams(method, streamType string) {
	ActiveStreams.WithLabelValues(method, streamType).Inc()
}

func DecActiveStreams(method, streamType string) {
	ActiveStreams.WithLabelValues(method, streamType).Dec()
}

func SetConcurrentRequests(count int) {
	ConcurrentRequestsGauge.Set(float64(count))
}

func RecordQueueTime(method string, queueTime time.Duration) {
	QueueTimeSeconds.WithLabelValues(method).Observe(queueTime.Seconds())
}

// StartMetricsServer starts the Prometheus metrics HTTP server
func StartMetricsServer(port string) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	log.Info().Str("port", port).Msg("Starting metrics server")
	return http.ListenAndServe(":"+port, mux)
}
