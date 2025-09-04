package livestatus

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds all the Prometheus collectors for the livestatus actor.
type Metrics struct {
	// Queue metrics
	queueLength   *prometheus.GaugeVec
	queueCapacity *prometheus.GaugeVec

	// Enqueueing & drops
	enqueuedTotal *prometheus.CounterVec
	droppedTotal  *prometheus.CounterVec

	// In-flight and processing time
	inFlight          *prometheus.GaugeVec
	processingSeconds *prometheus.HistogramVec

	// Results & health
	processedTotal           *prometheus.CounterVec
	panicsTotal              *prometheus.CounterVec
	lastSuccessTimestampSecs *prometheus.GaugeVec

	// Connection lifecycle
	reconnectsTotal *prometheus.CounterVec

	// Connectivity metrics (mirrors http exporter style)
	clientConnected        *prometheus.GaugeVec
	clientConnUptimeSecs   *prometheus.GaugeVec
	clientConnDurationSecs *prometheus.GaugeVec
	connectionDialsTotal   *prometheus.CounterVec
	connectionErrorsTotal  *prometheus.CounterVec
	siteName               string
}

// NewMetrics creates and registers all Prometheus collectors for the livestatus actor.
// If reg is nil, prometheus.DefaultRegisterer is used.
func NewMetrics(reg prometheus.Registerer, siteName string) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	m := &Metrics{
		siteName: siteName,
		// Queue metrics
		queueLength: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "livestatus",
				Subsystem: "actor",
				Name:      "queue_length",
				Help:      "Current number of items buffered in the actor's internal channel",
			},
			[]string{"site"}, // Label: site
		),
		queueCapacity: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "livestatus",
				Subsystem: "actor",
				Name:      "queue_capacity",
				Help:      "Fixed capacity of the buffered channel",
			},
			[]string{"site"}, // Label: site
		),

		// Enqueueing & drops
		enqueuedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "livestatus",
				Subsystem: "actor",
				Name:      "enqueued_total",
				Help:      "Total queries successfully accepted into the queue",
			},
			[]string{"site"}, // Label: site
		),
		droppedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "livestatus",
				Subsystem: "actor",
				Name:      "dropped_total",
				Help:      "Total queries not accepted",
			},
			[]string{"site", "reason"}, // site label + reason
		),

		// In-flight and processing time
		inFlight: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "livestatus",
				Subsystem: "actor",
				Name:      "in_flight",
				Help:      "Number of items currently being processed by the single worker",
			},
			[]string{"site"},
		),
		processingSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "livestatus",
				Subsystem: "actor",
				Name:      "processing_seconds",
				Help:      "End-to-end latency per processed work item",
				Buckets:   []float64{0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10},
			},
			[]string{"site"},
		),

		// Results & health
		processedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "livestatus",
				Subsystem: "actor",
				Name:      "processed_total",
				Help:      "Outcome count for processed items, by final status",
			},
			[]string{"site", "status"},
		),
		panicsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "livestatus",
				Subsystem: "actor",
				Name:      "panics_total",
				Help:      "Number of recovered panics while processing work",
			},
			[]string{"site"},
		),
		lastSuccessTimestampSecs: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "livestatus",
				Subsystem: "actor",
				Name:      "last_success_timestamp_seconds",
				Help:      "UNIX timestamp of the most recent successful item",
			},
			[]string{"site"},
		),

		// Connection lifecycle
		reconnectsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "livestatus",
				Subsystem: "actor",
				Name:      "reconnects_total",
				Help:      "Counts connection lifecycle events (established, closed, failures)",
			},
			[]string{"site", "reason"},
		),

		// Connectivity metrics
		clientConnected: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "livestatus",
				Subsystem: "actor",
				Name:      "client_connected",
				Help:      "Whether the actor's TCP client is currently connected (1) or not (0)",
			},
			[]string{"site"}, // Variable label for actor/site
		),
		clientConnUptimeSecs: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "livestatus",
				Subsystem: "actor",
				Name:      "client_connection_uptime_seconds",
				Help:      "How long the current connection has been up (seconds)",
			},
			[]string{"site"}, // Variable label for actor/site
		),
		clientConnDurationSecs: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "livestatus",
				Subsystem: "actor",
				Name:      "client_connection_duration_seconds",
				Help:      "Duration of the most recently closed connection (seconds)",
			},
			[]string{"site"}, // Variable label for actor/site
		),
		connectionDialsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "livestatus",
				Subsystem: "actor",
				Name:      "connection_dials_total",
				Help:      "Total number of successful TCP dials",
			},
			[]string{"site"}, // Variable label for actor/site
		),
		connectionErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "livestatus",
				Subsystem: "actor",
				Name:      "connection_errors_total",
				Help:      "Total number of connection/probe errors",
			},
			[]string{"site"}, // Variable label for actor/site
		),
	}

	// Register all metrics, reusing existing collectors if already registered
	if err := reg.Register(m.queueLength); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			m.queueLength = are.ExistingCollector.(*prometheus.GaugeVec)
		} else {
			panic(err)
		}
	}
	if err := reg.Register(m.queueCapacity); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			m.queueCapacity = are.ExistingCollector.(*prometheus.GaugeVec)
		} else {
			panic(err)
		}
	}
	if err := reg.Register(m.enqueuedTotal); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			m.enqueuedTotal = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			panic(err)
		}
	}
	if err := reg.Register(m.droppedTotal); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			m.droppedTotal = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			panic(err)
		}
	}
	if err := reg.Register(m.inFlight); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			m.inFlight = are.ExistingCollector.(*prometheus.GaugeVec)
		} else {
			panic(err)
		}
	}
	if err := reg.Register(m.processingSeconds); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			m.processingSeconds = are.ExistingCollector.(*prometheus.HistogramVec)
		} else {
			panic(err)
		}
	}
	if err := reg.Register(m.processedTotal); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			m.processedTotal = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			panic(err)
		}
	}
	if err := reg.Register(m.panicsTotal); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			m.panicsTotal = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			panic(err)
		}
	}
	if err := reg.Register(m.lastSuccessTimestampSecs); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			m.lastSuccessTimestampSecs = are.ExistingCollector.(*prometheus.GaugeVec)
		} else {
			panic(err)
		}
	}
	if err := reg.Register(m.reconnectsTotal); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			m.reconnectsTotal = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			panic(err)
		}
	}
	if err := reg.Register(m.clientConnected); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			m.clientConnected = are.ExistingCollector.(*prometheus.GaugeVec)
		} else {
			panic(err)
		}
	}
	if err := reg.Register(m.clientConnUptimeSecs); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			m.clientConnUptimeSecs = are.ExistingCollector.(*prometheus.GaugeVec)
		} else {
			panic(err)
		}
	}
	if err := reg.Register(m.clientConnDurationSecs); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			m.clientConnDurationSecs = are.ExistingCollector.(*prometheus.GaugeVec)
		} else {
			panic(err)
		}
	}
	if err := reg.Register(m.connectionDialsTotal); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			m.connectionDialsTotal = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			panic(err)
		}
	}
	if err := reg.Register(m.connectionErrorsTotal); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			m.connectionErrorsTotal = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			panic(err)
		}
	}

	return m
}

// UpdateQueueLength updates the queue length metric
func (m *Metrics) UpdateQueueLength(length int) {
	m.queueLength.WithLabelValues(m.siteName).Set(float64(length))
}

// SetQueueCapacity sets the queue capacity metric (called once at startup)
func (m *Metrics) SetQueueCapacity(capacity int) {
	m.queueCapacity.WithLabelValues(m.siteName).Set(float64(capacity))
}

// IncrementEnqueued increments the enqueued counter
func (m *Metrics) IncrementEnqueued() {
	m.enqueuedTotal.WithLabelValues(m.siteName).Inc()
}

// IncrementDropped increments the dropped counter with reason
func (m *Metrics) IncrementDropped(reason string) {
	m.droppedTotal.WithLabelValues(m.siteName, reason).Inc()
}

// IncrementInFlight increments the in-flight gauge
func (m *Metrics) IncrementInFlight() {
	m.inFlight.WithLabelValues(m.siteName).Inc()
}

// DecrementInFlight decrements the in-flight gauge
func (m *Metrics) DecrementInFlight() {
	m.inFlight.WithLabelValues(m.siteName).Dec()
}

// ObserveProcessingSeconds observes processing duration
func (m *Metrics) ObserveProcessingSeconds(duration float64) {
	m.processingSeconds.WithLabelValues(m.siteName).Observe(duration)
}

// IncrementProcessed increments the processed counter with status
func (m *Metrics) IncrementProcessed(status string) {
	m.processedTotal.WithLabelValues(m.siteName, status).Inc()
}

// IncrementPanics increments the panics counter
func (m *Metrics) IncrementPanics() {
	m.panicsTotal.WithLabelValues(m.siteName).Inc()
}

// UpdateLastSuccessTimestamp updates the last success timestamp
func (m *Metrics) UpdateLastSuccessTimestamp() {
	m.lastSuccessTimestampSecs.WithLabelValues(m.siteName).SetToCurrentTime()
}

// IncrementReconnects increments the reconnects counter with reason
func (m *Metrics) IncrementReconnects(reason string) {
	m.reconnectsTotal.WithLabelValues(m.siteName, reason).Inc()
}

// Connectivity helpers

func (m *Metrics) SetClientConnected(on int) {
	m.clientConnected.WithLabelValues(m.siteName).Set(float64(on))
}

func (m *Metrics) SetClientConnUptime(seconds float64) {
	m.clientConnUptimeSecs.WithLabelValues(m.siteName).Set(seconds)
}

func (m *Metrics) SetClientConnDuration(seconds float64) {
	m.clientConnDurationSecs.WithLabelValues(m.siteName).Set(seconds)
}

func (m *Metrics) IncrementConnectionDials() {
	m.connectionDialsTotal.WithLabelValues(m.siteName).Inc()
}

func (m *Metrics) IncrementConnectionErrors() {
	m.connectionErrorsTotal.WithLabelValues(m.siteName).Inc()
}
