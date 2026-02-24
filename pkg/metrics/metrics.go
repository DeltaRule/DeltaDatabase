// Package metrics provides Prometheus metric definitions and a metrics HTTP
// server for DeltaDatabase workers.
//
// Usage:
//
//	// In main-worker:
//	m := metrics.NewMainWorkerMetrics()
//	go m.Serve(":9090")
//
//	// In proc-worker:
//	m := metrics.NewProcWorkerMetrics()
//	go m.Serve(":9091")
package metrics

import (
	"log"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MainWorkerMetrics holds all Prometheus metrics for the Main Worker.
type MainWorkerMetrics struct {
	// gRPC – Subscribe RPC
	SubscribeRequestsTotal   *prometheus.CounterVec
	SubscribeDurationSeconds *prometheus.HistogramVec

	// gRPC – Process RPC (GET / PUT forwarded to proc-workers)
	ProcessRequestsTotal   *prometheus.CounterVec
	ProcessDurationSeconds *prometheus.HistogramVec

	// REST HTTP
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPDurationSeconds *prometheus.HistogramVec

	// Auth / session counters
	ActiveWorkerTokens prometheus.Gauge
	ActiveClientTokens prometheus.Gauge
	RegisteredWorkers  prometheus.Gauge
	AvailableWorkers   prometheus.Gauge

	// In-memory entity LRU cache
	EntityCacheSize           prometheus.Gauge
	EntityCacheHitsTotal      prometheus.Counter
	EntityCacheMissesTotal    prometheus.Counter
	EntityCacheEvictionsTotal prometheus.Counter

	registry *prometheus.Registry
}

// NewMainWorkerMetrics registers and returns a new MainWorkerMetrics instance
// backed by its own Prometheus registry.  All metrics use the
// "deltadatabase_main" namespace.
func NewMainWorkerMetrics() *MainWorkerMetrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &MainWorkerMetrics{
		registry: reg,

		SubscribeRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "deltadatabase",
			Subsystem: "main",
			Name:      "subscribe_requests_total",
			Help:      "Total number of Subscribe gRPC requests received by the Main Worker.",
		}, []string{"status"}),

		SubscribeDurationSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "deltadatabase",
			Subsystem: "main",
			Name:      "subscribe_request_duration_seconds",
			Help:      "Duration of Subscribe gRPC requests in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"status"}),

		ProcessRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "deltadatabase",
			Subsystem: "main",
			Name:      "process_requests_total",
			Help:      "Total number of Process gRPC requests received by the Main Worker.",
		}, []string{"operation", "status"}),

		ProcessDurationSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "deltadatabase",
			Subsystem: "main",
			Name:      "process_request_duration_seconds",
			Help:      "Duration of Process gRPC requests in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"operation", "status"}),

		HTTPRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "deltadatabase",
			Subsystem: "main",
			Name:      "http_requests_total",
			Help:      "Total number of HTTP requests received by the Main Worker REST server.",
		}, []string{"method", "path", "status_code"}),

		HTTPDurationSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "deltadatabase",
			Subsystem: "main",
			Name:      "http_request_duration_seconds",
			Help:      "Duration of HTTP requests served by the Main Worker REST server.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method", "path"}),

		ActiveWorkerTokens: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "deltadatabase",
			Subsystem: "main",
			Name:      "active_worker_tokens",
			Help:      "Current number of active (non-expired) Processing Worker session tokens.",
		}),

		ActiveClientTokens: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "deltadatabase",
			Subsystem: "main",
			Name:      "active_client_tokens",
			Help:      "Current number of active (non-expired) client session tokens.",
		}),

		RegisteredWorkers: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "deltadatabase",
			Subsystem: "main",
			Name:      "registered_workers",
			Help:      "Number of Processing Workers registered with the Main Worker.",
		}),

		AvailableWorkers: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "deltadatabase",
			Subsystem: "main",
			Name:      "available_workers",
			Help:      "Number of Processing Workers currently in the Available state.",
		}),

		EntityCacheSize: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "deltadatabase",
			Subsystem: "main",
			Name:      "entity_cache_size",
			Help:      "Current number of entries in the Main Worker's in-memory entity LRU cache.",
		}),

		EntityCacheHitsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "deltadatabase",
			Subsystem: "main",
			Name:      "entity_cache_hits_total",
			Help:      "Total number of entity cache hits on the Main Worker.",
		}),

		EntityCacheMissesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "deltadatabase",
			Subsystem: "main",
			Name:      "entity_cache_misses_total",
			Help:      "Total number of entity cache misses on the Main Worker.",
		}),

		EntityCacheEvictionsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "deltadatabase",
			Subsystem: "main",
			Name:      "entity_cache_evictions_total",
			Help:      "Total number of entity cache evictions on the Main Worker.",
		}),
	}

	reg.MustRegister(
		m.SubscribeRequestsTotal,
		m.SubscribeDurationSeconds,
		m.ProcessRequestsTotal,
		m.ProcessDurationSeconds,
		m.HTTPRequestsTotal,
		m.HTTPDurationSeconds,
		m.ActiveWorkerTokens,
		m.ActiveClientTokens,
		m.RegisteredWorkers,
		m.AvailableWorkers,
		m.EntityCacheSize,
		m.EntityCacheHitsTotal,
		m.EntityCacheMissesTotal,
		m.EntityCacheEvictionsTotal,
	)

	return m
}

// Serve starts an HTTP server exposing the /metrics endpoint on addr.
// It blocks until the server exits and logs any error.
func (m *MainWorkerMetrics) Serve(addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	log.Printf("Main Worker Prometheus metrics server listening on %s/metrics", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("Main Worker metrics server error: %v", err)
	}
}

// ProcWorkerMetrics holds all Prometheus metrics for the Processing Worker.
type ProcWorkerMetrics struct {
	// gRPC – Process RPC (GET / PUT)
	ProcessRequestsTotal   *prometheus.CounterVec
	ProcessDurationSeconds *prometheus.HistogramVec

	// In-memory LRU cache
	CacheHitsTotal      prometheus.Counter
	CacheMissesTotal    prometheus.Counter
	CacheEvictionsTotal prometheus.Counter
	CacheSize           prometheus.Gauge

	// Asynchronous disk writes
	AsyncWritesPending      prometheus.Gauge
	AsyncWritesTotal        prometheus.Counter
	AsyncWriteFailuresTotal prometheus.Counter

	// Encryption / decryption operations
	EncryptionOperationsTotal *prometheus.CounterVec
	EncryptionFailuresTotal   *prometheus.CounterVec

	// Subscription handshake
	HandshakeAttemptsTotal *prometheus.CounterVec

	registry *prometheus.Registry
}

// NewProcWorkerMetrics registers and returns a new ProcWorkerMetrics instance
// backed by its own Prometheus registry.  All metrics use the
// "deltadatabase_proc" namespace.
func NewProcWorkerMetrics() *ProcWorkerMetrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &ProcWorkerMetrics{
		registry: reg,

		ProcessRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "deltadatabase",
			Subsystem: "proc",
			Name:      "process_requests_total",
			Help:      "Total number of Process gRPC requests handled by the Processing Worker.",
		}, []string{"operation", "status"}),

		ProcessDurationSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "deltadatabase",
			Subsystem: "proc",
			Name:      "process_request_duration_seconds",
			Help:      "Duration of Process gRPC requests handled by the Processing Worker.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"operation"}),

		CacheHitsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "deltadatabase",
			Subsystem: "proc",
			Name:      "cache_hits_total",
			Help:      "Total number of in-memory cache hits on the Processing Worker.",
		}),

		CacheMissesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "deltadatabase",
			Subsystem: "proc",
			Name:      "cache_misses_total",
			Help:      "Total number of in-memory cache misses on the Processing Worker.",
		}),

		CacheEvictionsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "deltadatabase",
			Subsystem: "proc",
			Name:      "cache_evictions_total",
			Help:      "Total number of in-memory cache evictions on the Processing Worker.",
		}),

		CacheSize: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "deltadatabase",
			Subsystem: "proc",
			Name:      "cache_size",
			Help:      "Current number of entries in the Processing Worker's in-memory LRU cache.",
		}),

		AsyncWritesPending: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "deltadatabase",
			Subsystem: "proc",
			Name:      "async_writes_pending",
			Help:      "Number of asynchronous disk-write goroutines currently in-flight.",
		}),

		AsyncWritesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "deltadatabase",
			Subsystem: "proc",
			Name:      "async_writes_total",
			Help:      "Total number of asynchronous disk writes attempted by the Processing Worker.",
		}),

		AsyncWriteFailuresTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "deltadatabase",
			Subsystem: "proc",
			Name:      "async_write_failures_total",
			Help:      "Total number of failed asynchronous disk writes.",
		}),

		EncryptionOperationsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "deltadatabase",
			Subsystem: "proc",
			Name:      "encryption_operations_total",
			Help:      "Total number of encryption/decryption operations performed by the Processing Worker.",
		}, []string{"operation"}), // operation: "encrypt" | "decrypt"

		EncryptionFailuresTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "deltadatabase",
			Subsystem: "proc",
			Name:      "encryption_failures_total",
			Help:      "Total number of failed encryption/decryption operations.",
		}, []string{"operation"}),

		HandshakeAttemptsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "deltadatabase",
			Subsystem: "proc",
			Name:      "handshake_attempts_total",
			Help:      "Total number of Subscribe handshake attempts with the Main Worker.",
		}, []string{"status"}), // status: "success" | "error"
	}

	reg.MustRegister(
		m.ProcessRequestsTotal,
		m.ProcessDurationSeconds,
		m.CacheHitsTotal,
		m.CacheMissesTotal,
		m.CacheEvictionsTotal,
		m.CacheSize,
		m.AsyncWritesPending,
		m.AsyncWritesTotal,
		m.AsyncWriteFailuresTotal,
		m.EncryptionOperationsTotal,
		m.EncryptionFailuresTotal,
		m.HandshakeAttemptsTotal,
	)

	return m
}

// Serve starts an HTTP server exposing the /metrics endpoint on addr.
// It blocks until the server exits and logs any error.
func (m *ProcWorkerMetrics) Serve(addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	log.Printf("Processing Worker Prometheus metrics server listening on %s/metrics", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("Processing Worker metrics server error: %v", err)
	}
}

// RegisterGoCollectors is kept for backwards compatibility but is no longer
// needed when using NewMainWorkerMetrics / NewProcWorkerMetrics because each
// creates its own registry with Go and process collectors already registered.
func RegisterGoCollectors() {}

// ServeMetrics is kept for backwards compatibility.  Prefer using the Serve
// method on the individual metrics struct instead.
func ServeMetrics(addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	log.Printf("Prometheus metrics server listening on %s/metrics", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("Metrics server error: %v", err)
	}
}
