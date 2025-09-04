package livestatus

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// LiveStatusActor manages a queue and processes livestatus queries.
// Results are published to a shared, caller-owned results channel.
type LiveStatusActor struct {
	logger   *slog.Logger
	siteName string
	metrics  *Metrics
	config   *LiveStatusConfig

	// Queue and processing
	queue     chan *WorkItem
	results   chan<- ResultMsg // caller-owned bus
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once

	// Configuration
	queueCapacity int

	// Persistent connection state (single worker => no extra locking required)
	conn   net.Conn
	reader *bufio.Reader

	// Connectivity events and tracking
	eventChan   chan<- ConnectivityEvent
	activeSince time.Time
	connState   atomic.Int32
}

// SendQuery creates a work item from the given query, enqueues it, and returns the request ID.
func (a *LiveStatusActor) SendQuery(ctx context.Context, query LiveStatusQuery) (RequestID, error) {
	id := nextRequestID()
	item := NewWorkItemFromQuery(id, &query)
	if err := a.Enqueue(ctx, item); err != nil {
		return 0, err
	}
	return id, nil
}

// TrySendQuery attempts to enqueue a query without blocking.
// It returns the generated RequestID and true on success, or 0 and false if the queue is full.
func (a *LiveStatusActor) TrySendQuery(query LiveStatusQuery) (RequestID, bool) {
	id := nextRequestID()
	item := NewWorkItemFromQuery(id, &query)
	if a.TryEnqueue(item) {
		return id, true
	}
	return 0, false
}

// NewLiveStatusActor creates a new livestatus actor.
//
// The 'results' channel is owned by the caller; the actor will publish
// ResultMsg values to it but will never close it.
func NewLiveStatusActor(
	logger *slog.Logger,
	siteName string,
	config *LiveStatusConfig,
	queueCapacity int,
	results chan<- ResultMsg,
	reg prometheus.Registerer,

) *LiveStatusActor {
	if queueCapacity <= 0 {
		queueCapacity = 100 // Default capacity
	}
	if results == nil {
		panic("results channel must not be nil")
	}

	actor := &LiveStatusActor{
		logger:        logger.With("scope", "LiveStatusActor", "site", siteName),
		siteName:      siteName,
		queue:         make(chan *WorkItem, queueCapacity),
		results:       results,
		ctx:           nil,
		cancel:        nil,
		queueCapacity: queueCapacity,
		config:        config,
	}

	// Create metrics
	actor.metrics = NewMetrics(reg, siteName)

	// Set initial capacity metric
	actor.metrics.SetQueueCapacity(queueCapacity)

	// initial connectivity metrics
	actor.metrics.SetClientConnected(0)

	return actor
}

// SetEventChan configures an optional event channel for connectivity events.
func (a *LiveStatusActor) SetEventChan(ch chan<- ConnectivityEvent) {
	a.eventChan = ch
}

func (a *LiveStatusActor) setState(s ConnectivityState) {
	a.connState.Store(int32(s))
}

func (a *LiveStatusActor) emit(ev ConnectivityEvent) {
	a.setState(ev.State)
	if a.eventChan == nil {
		return
	}
	select {
	case a.eventChan <- ev:
	default:
	}
}

// Start begins the processing worker. It is safe to call only once; subsequent calls return an error.
func (a *LiveStatusActor) Start(ctx context.Context) error {
	a.logger.Debug("Start")
	if ctx == nil {
		return fmt.Errorf("ctx cannot be nil")
	}
	// If already started, return error
	if a.ctx != nil {
		return fmt.Errorf("actor already started")
	}
	a.ctx, a.cancel = context.WithCancel(ctx)
	a.wg.Add(1)
	go a.processLoop()
	return nil
}

// TryEnqueue attempts to enqueue a work item without blocking.
func (a *LiveStatusActor) TryEnqueue(item *WorkItem) bool {
	select {
	case a.queue <- item:
		a.metrics.IncrementEnqueued()
		a.metrics.UpdateQueueLength(len(a.queue))
		return true
	default:
		a.metrics.IncrementDropped("queue_full")
		return false
	}
}

// Enqueue enqueues a work item, blocking until space is available or context is cancelled.
func (a *LiveStatusActor) Enqueue(ctx context.Context, item *WorkItem) error {
	// Fast-path: if caller's context is already canceled, respect it immediately
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			a.metrics.IncrementDropped("ctx_done")
			return err
		}
	}
	// If actor not started yet, still allow enqueue (items will be processed after Start)
	if a.ctx == nil {
		select {
		case a.queue <- item:
			a.metrics.IncrementEnqueued()
			a.metrics.UpdateQueueLength(len(a.queue))
			return nil
		case <-ctx.Done():
			a.metrics.IncrementDropped("ctx_done")
			return ctx.Err()
		}
	}
	select {
	case a.queue <- item:
		a.metrics.IncrementEnqueued()
		a.metrics.UpdateQueueLength(len(a.queue))
		return nil
	case <-ctx.Done():
		a.metrics.IncrementDropped("ctx_done")
		return ctx.Err()
	case <-a.ctx.Done():
		a.metrics.IncrementDropped("actor_closed")
		return fmt.Errorf("actor is closed")
	}
}

// Close gracefully shuts down the actor. It does not close the results channel
// (caller-owned), but stops accepting and processing new work.
func (a *LiveStatusActor) Close() {
	a.closeOnce.Do(func() {
		if a.cancel != nil {
			a.cancel()
		}
		a.closeConn()
		close(a.queue)
	})
	a.wg.Wait()
}

// processLoop runs the main processing loop.
func (a *LiveStatusActor) processLoop() {
	logger := a.logger.With("scope", "processLoop")
	logger.Debug("START")
	defer logger.Debug("FINISHED")
	defer a.wg.Done()

	// Dynamic health-check cadence: faster when not connected, slower when healthy
	const (
		intervalDisconnected = 5 * time.Second
		intervalConnected    = 17 * time.Second
	)

	// Use a timer so we can adapt the interval
	timer := time.NewTimer(intervalDisconnected)
	defer timer.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-timer.C:
			a.runHealthCheck()
			// Reset cadence based on current connectivity state
			if ConnectivityState(a.connState.Load()) == StateConnected {
				if !timer.Stop() { /* channel already drained by receive above */
				}
				timer.Reset(intervalConnected)
			} else {
				if !timer.Stop() {
				}
				timer.Reset(intervalDisconnected)
			}
		case item, ok := <-a.queue:
			if !ok {
				return // Channel closed
			}

			a.metrics.UpdateQueueLength(len(a.queue))
			a.processItem(item)
		}
	}
}

// processItem processes a single work item with metrics and panic safety.
func (a *LiveStatusActor) processItem(item *WorkItem) {
	// Track in-flight
	a.metrics.IncrementInFlight()

	// Start timing
	timer := prometheus.NewTimer(prometheus.ObserverFunc(func(duration float64) {
		a.metrics.ObserveProcessingSeconds(duration)
	}))
	defer timer.ObserveDuration()

	// Process with panic recovery
	func() {
		defer func() {
			if r := recover(); r != nil {
				a.logger.Error("Panic while processing livestatus query",
					"panic", r,
					"query", item.Query,
					"id", item.ID,
				)
				a.metrics.IncrementPanics()
				a.publishResult(item.ID, &Result{StatusCode: 500, Error: fmt.Errorf("panic: %v", r)})
			}
		}()

		a.processOne(item)
	}()

	// Decrement in-flight
	a.metrics.DecrementInFlight()
}

// processOne implements the actual livestatus logic (simulated here).
func (a *LiveStatusActor) processOne(item *WorkItem) {
	var result *Result

	// If a real config is provided, execute the query against LiveStatus.
	if a.config != nil {
		// Single attempt; rely on health-check to maintain connectivity
		conn, reader, err := ensureConn(a.ctx, a.config, a.conn)
		if err != nil {
			result = &Result{StatusCode: 500, Error: err}
		} else {
			a.conn, a.reader = conn, reader
			res, err := execOverPersistentConn(a.logger, a.ctx, a.config, a.conn, a.reader, item.Query)
			if err != nil {
				result = &Result{StatusCode: 500, Error: err}
			} else {
				result = res
			}
		}
	} else {
		// Fallback simulation (keeps tests fast without requiring a LiveStatus endpoint)
		time.Sleep(50 * time.Millisecond)

		switch {
		case string(item.Query.table) == "error":
			result = &Result{StatusCode: StatusInternalServerError, Error: fmt.Errorf("simulated error")}
		case string(item.Query.table) == "not_found":
			result = &Result{StatusCode: StatusNotFound, Error: fmt.Errorf("not found")}
		default:
			result = &Result{StatusCode: StatusOK, Data: fmt.Appendf(nil, "Processed: %s", item.Query.Build())}
		}
	}

	// Update metrics based on result: track numeric HTTP-like status code
	statusCodeStr := strconv.Itoa(result.StatusCode)
	a.metrics.IncrementProcessed(statusCodeStr)

	// Update last success timestamp for successful responses
	if result.StatusCode == StatusOK {
		a.metrics.UpdateLastSuccessTimestamp()
	}

	// Publish result (non-blocking)
	a.publishResult(item.ID, result)
}

func (a *LiveStatusActor) closeConn() {
	if a.conn != nil {
		_ = a.conn.Close()
	}
	a.conn = nil
	a.reader = nil
	// track closes
	if !a.activeSince.IsZero() {
		d := time.Since(a.activeSince).Seconds()
		a.metrics.SetClientConnDuration(d)
	}
	a.activeSince = time.Time{}
	a.metrics.SetClientConnected(0)
	a.metrics.SetClientConnUptime(0)
	a.emit(ConnectivityEvent{Actor: a.siteName, State: StateDisconnected, Time: time.Now(), Reason: "closed"})
	a.metrics.IncrementReconnects("closed")
}

// runHealthCheck periodically ensures the connection is healthy in real mode.
func (a *LiveStatusActor) runHealthCheck() {
	// Only run health checks when there is no pending work
	if len(a.queue) > 0 {
		a.logger.Debug("health-check skipped: pending work", "pending", len(a.queue))
		return
	}
	if a.config == nil {
		a.logger.Debug("Missing config")
		return
	}
	a.logger.Debug("health-check tick", "actor", a.siteName)
	query := NewLiveStatusQuery(Table("hosts")).Columns("name").Limit(1)
	prevNil := (a.conn == nil)
	a.logger.Debug("health-check ensureConn", "addr", a.config.Address, "prevNil", prevNil)
	conn, reader, err := ensureConn(a.ctx, a.config, a.conn)
	if err != nil {
		a.logger.Warn("health-check connection failed", "err", err)
		a.metrics.IncrementReconnects("conn_error")
		a.metrics.IncrementConnectionErrors()
		a.emit(ConnectivityEvent{Actor: a.siteName, State: StateRetrying, Time: time.Now(), Reason: "conn_error", Err: err})
		a.metrics.SetClientConnected(0)
		a.closeConn()
		return
	}
	a.conn, a.reader = conn, reader
	if prevNil && a.conn != nil {
		a.logger.Debug("health-check connection established", "addr", a.config.Address)
		a.metrics.IncrementReconnects("established")
		a.metrics.IncrementConnectionDials()
		a.metrics.SetClientConnected(1)
		a.activeSince = time.Now()
		a.emit(ConnectivityEvent{Actor: a.siteName, State: StateConnected, Time: a.activeSince, Reason: "established"})
	}
	a.logger.Debug("health-check probe", "table", "hosts", "limit", 1)
	start := time.Now()
	if _, err := execOverPersistentConn(a.logger, a.ctx, a.config, a.conn, a.reader, *query); err != nil {
		a.logger.Warn("health-check query failed", "err", err, "duration", time.Since(start))
		a.metrics.IncrementReconnects("probe_error")
		a.metrics.IncrementConnectionErrors()
		a.emit(ConnectivityEvent{Actor: a.siteName, State: StateRetrying, Time: time.Now(), Reason: "probe_error", Err: err})
		a.closeConn()
	} else {
		a.logger.Debug("health-check ok", "duration", time.Since(start))
		if !a.activeSince.IsZero() {
			a.metrics.SetClientConnUptime(time.Since(a.activeSince).Seconds())
		}
	}
	a.logger.Debug("health-check response")
}

// publishResult tries to deliver the result to the shared results bus without blocking.
func (a *LiveStatusActor) publishResult(id RequestID, res *Result) {
	env := ResultMsg{ID: id, Result: res}
	select {
	case a.results <- env:
	default:
		// Results bus full — drop on floor and count it.
		a.metrics.IncrementDropped("result_chan_full")
	}
}
