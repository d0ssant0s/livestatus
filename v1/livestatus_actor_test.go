package livestatus

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/matryer/is"
	"github.com/prometheus/client_golang/prometheus"
)

// helper: receive with timeout from the shared results bus
func recvResult(t *testing.T, ch <-chan ResultMsg, timeout time.Duration) ResultMsg {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for result")
		return ResultMsg{}
	}
}

func TestNewLiveStatusActor(t *testing.T) {
	is := is.New(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	reg := prometheus.NewRegistry()
	results := make(chan ResultMsg, 16)

	var cfg *LiveStatusConfig = nil
	actor := NewLiveStatusActor(logger, "test_new_actor", cfg, 10, results, reg)
	defer actor.Close()

	is.Equal(actor.siteName, "test_new_actor")
	is.Equal(actor.queueCapacity, 10)
}

func TestBasicEnqueue(t *testing.T) {
	is := is.New(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	reg := prometheus.NewRegistry()
	results := make(chan ResultMsg, 4)

	var cfg *LiveStatusConfig = nil
	actor := NewLiveStatusActor(logger, "test_basic_enqueue", cfg, 2, results, reg)
	defer actor.Close()
	is.NoErr(actor.Start(context.Background()))

	// Enqueue two items successfully
	id1 := nextRequestID()
	item1 := &WorkItem{ID: id1, Query: *NewLiveStatusQuery(Table("hosts"))}
	is.True(actor.TryEnqueue(item1))

	id2 := nextRequestID()
	item2 := &WorkItem{ID: id2, Query: *NewLiveStatusQuery(Table("services"))}
	is.True(actor.TryEnqueue(item2))

	// Third should fail (queue full)
	item3 := &WorkItem{ID: nextRequestID(), Query: *NewLiveStatusQuery(Table("status"))}
	is.True(!actor.TryEnqueue(item3))

	// Expect two results on the bus (IDs can arrive in any order)
	got := map[RequestID]bool{}
	for i := 0; i < 2; i++ {
		msg := recvResult(t, results, 500*time.Millisecond)
		got[msg.ID] = true
		is.True(msg.Result != nil)
	}
	is.True(got[id1])
	is.True(got[id2])
}

func TestEnqueueWithContext(t *testing.T) {
	is := is.New(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	reg := prometheus.NewRegistry()
	results := make(chan ResultMsg, 1)

	var cfg *LiveStatusConfig = nil
	actor := NewLiveStatusActor(logger, "test_context_enqueue", cfg, 5, results, reg)
	defer actor.Close()
	is.NoErr(actor.Start(context.Background()))

	// Cancelled context should fail fast
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	item := &WorkItem{ID: nextRequestID(), Query: *NewLiveStatusQuery(Table("hosts"))}
	err := actor.Enqueue(ctx, item)
	is.True(err != nil) // Should return error due to context cancellation
}

func TestProcessing(t *testing.T) {
	is := is.New(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	reg := prometheus.NewRegistry()
	results := make(chan ResultMsg, 1)

	var cfg *LiveStatusConfig = nil
	actor := NewLiveStatusActor(logger, "test_processing", cfg, 5, results, reg)
	defer actor.Close()
	is.NoErr(actor.Start(context.Background()))

	id := nextRequestID()
	item := &WorkItem{ID: id, Query: *NewLiveStatusQuery(Table("hosts"))}
	is.True(actor.TryEnqueue(item))

	msg := recvResult(t, results, 1*time.Second)
	is.Equal(msg.ID, id)
	is.True(msg.Result != nil)
	is.NoErr(msg.Result.Error)
	is.Equal(msg.Result.StatusCode, 200)
}

func TestProcessingErrors(t *testing.T) {
	is := is.New(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	reg := prometheus.NewRegistry()
	results := make(chan ResultMsg, 2)

	var cfg *LiveStatusConfig = nil
	actor := NewLiveStatusActor(logger, "test_processing_errors", cfg, 5, results, reg)
	defer actor.Close()
	is.NoErr(actor.Start(context.Background()))

	errID := nextRequestID()
	nfID := nextRequestID()
	is.True(actor.TryEnqueue(&WorkItem{ID: errID, Query: *NewLiveStatusQuery(Table("error"))}))
	is.True(actor.TryEnqueue(&WorkItem{ID: nfID, Query: *NewLiveStatusQuery(Table("not_found"))}))

	seen := map[RequestID]*Result{}
	for i := 0; i < 2; i++ {
		msg := recvResult(t, results, 1*time.Second)
		seen[msg.ID] = msg.Result
	}

	is.True(seen[errID] != nil)
	is.True(seen[errID].Error != nil)
	is.Equal(seen[errID].StatusCode, 500)

	is.True(seen[nfID] != nil)
	is.True(seen[nfID].Error != nil)
	is.Equal(seen[nfID].StatusCode, 404)
}

func TestMetrics(t *testing.T) {
	is := is.New(t)
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg, "test_metrics")
	is.True(metrics != nil)

	_, err := reg.Gather()
	is.NoErr(err)
}

func TestNewWorkItemFromQuery(t *testing.T) {
	is := is.New(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	reg := prometheus.NewRegistry()
	results := make(chan ResultMsg, 1)

	var cfg *LiveStatusConfig = nil
	actor := NewLiveStatusActor(logger, "test_query_builder_integration", cfg, 2, results, reg)
	defer actor.Close()
	is.NoErr(actor.Start(context.Background()))

	q := NewLiveStatusQuery(Table("hosts"), "name").OutputFormat(OutputJSON)
	// Build work item using helper
	qid := nextRequestID()
	item := NewWorkItemFromQuery(qid, q)

	is.True(actor.TryEnqueue(item))
	msg := recvResult(t, results, 1*time.Second)
	is.Equal(msg.ID, qid)
	is.True(msg.Result != nil)
}
