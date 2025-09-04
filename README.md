# Livestatus Package

A Go library for building and processing Livestatus Query Language (LQL) requests using the actor pattern. Provides a fluent API for safe query construction and an asynchronous actor for processing livestatus queries with built-in metrics and error handling.

## Features

- **Fluent Query Builder**: Type-safe, chainable API for constructing LQL requests
- **Security**: Safe value escaping to prevent header injection attacks
- **Actor Pattern**: Asynchronous processing with configurable queue capacity
- **Metrics**: Comprehensive Prometheus metrics for monitoring
- **Error Handling**: Robust panic recovery and error propagation
- **Extensibility**: Support for advanced headers and custom query parameters

## Quick Start

### 1. Basic Query Building

```go
package main

import (
    "fmt"
    "log/slog"
    "os"

    "go_examples/pkg/livestatus"
    "github.com/prometheus/client_golang/prometheus"
)

func main() {
    // Create a simple host query
    query := livestatus.NewLiveStatusQuery("hosts", "name", "state", "address").
        OutputFormat(livestatus.OutputJSON).
        Limit(10)

    fmt.Println(query.Build())
}
```

### 2. Advanced Query with Filters

```go
// Build a complex service query with filters
query := livestatus.NewLiveStatusQuery("services", "host_name", "description", "state").
    FilterEqual("state", "2").            // Critical services
    FilterRegexI("description", "http").   // HTTP services
    And(2).                               // state=2 AND description~http
    OutputFormat(livestatus.OutputCSV).
    ColumnHeaders(true).
    Limit(50)

fmt.Println(query.Build())
```

### 3. Using the Actor for Processing

```go
// Set up the actor
logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
reg := prometheus.NewRegistry()

actor := livestatus.NewLiveStatusActor(logger, "my_exporter", 100, reg)
defer actor.Close()

// Build and enqueue a query
query := livestatus.NewLiveStatusQuery("hosts", "name", "state").
    FilterEqual("state", "0").
    OutputFormat(livestatus.OutputJSON)

workItem := livestatus.NewWorkItemFromQuery(query, 1)

// Enqueue for processing
if actor.TryEnqueue(workItem) {
    // Wait for result
    result := <-workItem.Result
    if result.Error != nil {
        fmt.Printf("Error: %v\n", result.Error)
    } else {
        fmt.Printf("Status: %d, Data: %s\n", result.StatusCode, string(result.Data))
    }
}
```

### 4. One-Off Queries (Direct Connection)

For immediate queries without using the actor, you can connect directly to Checkmk:

```go
// Create configuration for direct connection
config := livestatus.NewLiveStatusConfig("localhost:6557")  // TCP connection
// OR for Unix socket:
// config := livestatus.NewLiveStatusConfig("/var/run/livestatus.sock")

// Optional: customize timeouts
config.ConnectTimeout = 5 * time.Second
config.ReadTimeout = 30 * time.Second

// Method 1: Using raw query string
ctx := context.Background()
result, err := livestatus.QueryOneOff(ctx, "GET hosts\nColumns: name state\n\n", config)
if err != nil {
    log.Fatal(err)
}

if result.Error != nil {
    fmt.Printf("Query error: %v\n", result.Error)
} else {
    fmt.Printf("Response: %s\n", string(result.Data))
}

// Method 2: Using query builder (recommended)
query := livestatus.NewLiveStatusQuery("services", "host_name", "description").
    FilterEqual("state", "2").
    OutputFormat(livestatus.OutputJSON).
    Limit(100)

result, err = livestatus.QueryOneOffFromBuilder(ctx, query, config)
if err != nil {
    log.Fatal(err)
}

if result.Error != nil {
    fmt.Printf("Query error: %v\n", result.Error)
} else {
    fmt.Printf("Status: %d, Data: %s\n", result.StatusCode, string(result.Data))
}
```

## API Reference

### Query Builder

#### Creating Queries

```go
// Basic query
query := livestatus.NewLiveStatusQuery("hosts", "name", "state")

// With different constructor options
query := livestatus.NewLiveStatusQuery(livestatus.Table("services"))
query = query.Columns("host_name", "description", "state")
```

#### Available Tables

The `Table` type represents livestatus tables:

- `"hosts"` - Host information
- `"services"` - Service information
- `"status"` - Status information
- `"hostgroups"` - Host group information
- `"servicegroups"` - Service group information
- And many more depending on your monitoring setup

#### Columns and Output

```go
// Set columns
query := livestatus.NewLiveStatusQuery("hosts").
    Columns("name", "address", "state").
    AddColumns("last_check", "next_check")

// Output formats
query = query.OutputFormat(livestatus.OutputJSON)   // JSON format
query = query.OutputFormat(livestatus.OutputCSV)    // CSV format
query = query.OutputFormat(livestatus.OutputPY)     // Python format

// Column headers (for CSV)
query = query.ColumnHeaders(true)  // Enable column headers
```

#### Filtering

```go
// Basic filters
query := livestatus.NewLiveStatusQuery("services").
    FilterEqual("state", "2").           // state = 2
    FilterNotEqual("acknowledged", "0"). // acknowledged != 0
    FilterGreaterThan("last_check", "0"). // last_check > 0
    FilterLessOrEqual("next_check", "0"). // next_check <= 0

// Regex filters
query = query.FilterRegex("description", "^http").        // Case-sensitive regex
query = query.FilterRegexI("description", "database").    // Case-insensitive regex

// Generic filter (for unsupported operators)
query = query.Filter("custom_field", livestatus.OpEq, "value")
```

#### Boolean Logic

```go
// Combine filters with AND/OR
query := livestatus.NewLiveStatusQuery("services").
    FilterEqual("state", "2").       // Filter 1: state = 2
    FilterEqual("acknowledged", "0"). // Filter 2: acknowledged = 0
    And(2).                          // (Filter 1 AND Filter 2)
    FilterRegexI("description", "web"). // Filter 3: description ~ web
    Or(2).                           // (Previous AND) OR Filter 3

// Negate filters
query = query.FilterEqual("active", "1").Negate() // NOT (active = 1)
```

#### Advanced Headers

```go
// Limit results
query := livestatus.NewLiveStatusQuery("hosts").
    Limit(100)

// Wait conditions (long-polling)
query = query.
    WaitTrigger("check").                    // Wait for check trigger
    WaitObject("host;web-server-01").        // Wait for specific object
    WaitCondition("state = 0").              // Wait for condition
    WaitTimeout(30000)                       // 30 second timeout

// Connection management
query = query.
    KeepAlive(true).                         // Keep connection alive
    ResponseHeaderFixed16()                  // Fixed 16-byte response header

// Custom headers
query = query.Header("Stats", "state = 2")          // Custom stats
query = query.Header("Localtime", "1640995200")     // Unix timestamp
```

### Actor Usage

#### Creating an Actor

```go
// Create with default capacity (100)
actor := livestatus.NewLiveStatusActor(logger, "my_exporter", 0, reg)

// Create with custom capacity
actor := livestatus.NewLiveStatusActor(logger, "my_exporter", 500, reg)
```

#### Enqueueing Work

```go
// Try to enqueue (non-blocking)
if actor.TryEnqueue(workItem) {
    // Successfully enqueued
} else {
    // Queue is full
}

// Enqueue with context (blocking with timeout)
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

if err := actor.Enqueue(ctx, workItem); err != nil {
    // Handle error (timeout, context cancelled, actor closed)
}
```

#### Processing Results

```go
// Synchronous result handling
result := <-workItem.Result
if result.Error != nil {
    log.Printf("Query error: %v", result.Error)
} else {
    log.Printf("Status: %d, Data length: %d", result.StatusCode, len(result.Data))
}

// Asynchronous result handling with timeout
select {
case result := <-workItem.Result:
    // Handle result
case <-time.After(10 * time.Second):
    // Timeout
}
```

### One-Off Query API

#### Configuration

```go
// Create configuration for TCP connection
config := livestatus.NewLiveStatusConfig("localhost:6557")

// Create configuration for Unix socket
config := livestatus.NewLiveStatusConfig("/var/run/livestatus.sock")

// Customize timeouts
config.ConnectTimeout = 5 * time.Second    // Connection establishment
config.ReadTimeout = 30 * time.Second      // Response reading
```

#### Direct Query Execution

```go
// Execute raw query string
result, err := livestatus.QueryOneOff(ctx, "GET hosts\nColumns: name state\n\n", config)

// Execute query built with LiveStatusQuery
query := livestatus.NewLiveStatusQuery("services", "host_name").
    FilterEqual("state", "2").
    OutputFormat(livestatus.OutputJSON)

result, err := livestatus.QueryOneOffFromBuilder(ctx, query, config)

// Handle result
if err != nil {
    // Function-level error (e.g., nil config)
    log.Fatal(err)
}

if result.Error != nil {
    // Query execution error (e.g., connection failure, invalid query)
    log.Printf("Query error: %v", result.Error)
} else {
    // Success
    log.Printf("Status: %d, Response: %s", result.StatusCode, string(result.Data))
}
```

## Metrics

The package provides comprehensive Prometheus metrics:

### Queue Metrics
- `livestatus_actor_queue_length` - Current queue length
- `livestatus_actor_queue_capacity` - Queue capacity

### Throughput Metrics
- `livestatus_actor_enqueued_total` - Total enqueued requests
- `livestatus_actor_dropped_total` - Total dropped requests (by reason)
- `livestatus_actor_processed_total` - Total processed requests (by status)

### Performance Metrics
- `livestatus_actor_in_flight` - Current in-flight requests
- `livestatus_actor_processing_seconds` - Processing time histogram
- `livestatus_actor_panics_total` - Total panic count
- `livestatus_actor_last_success_timestamp_secs` - Last successful request timestamp

#### Metric labels

All metrics consistently use the following labels:

- `site`: the Checkmk site name associated with the actor instance
- `reason`: only on some counters (e.g., `*_dropped_total`, `*_reconnects_total`) to classify cause
- `status`: only on `*_processed_total` to record the numeric status code as a string

Example series: `livestatus_actor_dropped_total{site="site-a",reason="queue_full"}`

## Security Considerations

### Safe Value Handling

The query builder automatically sanitizes values to prevent header injection:

```go
// These characters are automatically escaped
query := livestatus.NewLiveStatusQuery("hosts").
    FilterEqual("name", "bad\r\nhost").  // CRLF becomes space
    FilterEqual("name", "host\x00").     // NUL is removed

// Headers are also sanitized
query = query.Header("User-Agent", "value\nwith\nnewlines") // Newlines become spaces
```

### Best Practices

1. **Always use the query builder** - Don't construct raw LQL strings
2. **Validate input** - Sanitize user input before passing to filters
3. **Use appropriate timeouts** - Prevent hanging requests
4. **Monitor metrics** - Watch for dropped requests and panics
5. **Handle errors** - Always check for errors in results

## Error Handling

The actor provides robust error handling:

```go
// Actor-level errors
if err := actor.Enqueue(ctx, workItem); err != nil {
    switch err {
    case context.DeadlineExceeded:
        // Timeout
    case context.Canceled:
        // Context cancelled
    default:
        // Actor closed or other error
    }
}

// Query processing errors
result := <-workItem.Result
if result.Error != nil {
    // Query processing failed (e.g., network error, invalid query)
}

// HTTP-level errors
if result.StatusCode >= 400 {
    // HTTP error (400 Bad Request, 404 Not Found, etc.)
}
```

## Configuration

### Actor Configuration

```go
// High-throughput configuration
actor := livestatus.NewLiveStatusActor(
    logger,
    "high_throughput_exporter",
    1000,  // Large queue capacity
    reg,
)

// Low-latency configuration
actor := livestatus.NewLiveStatusActor(
    logger,
    "low_latency_exporter",
    10,   // Small queue capacity
    reg,
)
```

### Metrics Configuration

```go
// Use default registry
actor := livestatus.NewLiveStatusActor(logger, "exporter", 100, nil)

// Use custom registry
reg := prometheus.NewRegistry()
actor := livestatus.NewLiveStatusActor(logger, "exporter", 100, reg)
```

## Examples

### Monitoring Dashboard Query

```go
func buildDashboardQuery() *livestatus.LiveStatusQuery {
    return livestatus.NewLiveStatusQuery("services",
        "host_name",
        "description",
        "state",
        "last_check",
        "next_check",
        "plugin_output").
        FilterEqual("state", "2").              // Critical only
        FilterEqual("acknowledged", "0").       // Not acknowledged
        OutputFormat(livestatus.OutputJSON).
        ColumnHeaders(true).
        Limit(1000)
}
```

### Service Discovery Query

```go
func buildServiceDiscoveryQuery(serviceType string) *livestatus.LiveStatusQuery {
    return livestatus.NewLiveStatusQuery("services",
        "host_name",
        "address",
        "description").
        FilterRegexI("description", serviceType).
        FilterEqual("active", "1").              // Only active services
        OutputFormat(livestatus.OutputJSON).
        Limit(500)
}
```

### Long-polling Alert Query

```go
func buildAlertQuery() *livestatus.LiveStatusQuery {
    return livestatus.NewLiveStatusQuery("services",
        "host_name",
        "description",
        "state",
        "plugin_output").
        FilterEqual("state", "2").               // Critical state
        WaitTrigger("check").                    // Wait for check results
        WaitTimeout(60000).                      // 1 minute timeout
        OutputFormat(livestatus.OutputJSON)
}
```

### Direct Query Example

```go
func queryHostStatus(ctx context.Context, hostName string) (*livestatus.Result, error) {
    // Configure connection to Checkmk
    config := livestatus.NewLiveStatusConfig("localhost:6557")
    config.ConnectTimeout = 3 * time.Second
    config.ReadTimeout = 10 * time.Second

    // Build and execute query
    query := livestatus.NewLiveStatusQuery("hosts",
        "name",
        "state",
        "address",
        "last_check",
        "plugin_output").
        FilterEqual("name", hostName).
        OutputFormat(livestatus.OutputJSON)

    return livestatus.QueryOneOffFromBuilder(ctx, query, config)
}

// Usage
func main() {
    ctx := context.Background()
    result, err := queryHostStatus(ctx, "web-server-01")
    if err != nil {
        log.Fatal(err)
    }

    if result.Error != nil {
        fmt.Printf("Query failed: %v\n", result.Error)
    } else {
        fmt.Printf("Host status: %s\n", string(result.Data))
    }
}
```

## Troubleshooting

### Common Issues

1. **Queue Full**: Increase queue capacity or reduce request rate
2. **Timeouts**: Increase timeout values or check network connectivity
3. **Panics**: Check query syntax and monitor panic metrics
4. **High Memory Usage**: Monitor queue length and in-flight metrics

### Choosing Between Actor and One-Off Queries

**Use the Actor Pattern when:**
- You need high-throughput query processing
- You want built-in queuing and backpressure handling
- You need comprehensive metrics and monitoring
- You're building a long-running service
- You want automatic retry logic and error recovery

**Use One-Off Queries when:**
- You need immediate, synchronous query execution
- You're building a CLI tool or one-time script
- You want direct control over connection management
- You're doing ad-hoc queries or debugging
- You don't need queuing or complex error handling

**Example scenarios:**
```go
// Actor pattern - high throughput service
func monitoringService() {
    actor := livestatus.NewLiveStatusActor(logger, "monitor", 1000, reg)
    defer actor.Close()

    // Process many queries concurrently with queuing
    for query := range queryChannel {
        workItem := livestatus.NewWorkItemFromQuery(query, 1)
        actor.TryEnqueue(workItem)
    }
}

// One-off queries - CLI tool
func main() {
    config := livestatus.NewLiveStatusConfig("localhost:6557")

    // Execute single query and exit
    query := livestatus.NewLiveStatusQuery("hosts", "name").FilterEqual("state", "0")
    result, err := livestatus.QueryOneOffFromBuilder(context.Background(), query, config)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(string(result.Data))
}
```

### Debugging

```go
// Enable debug logging
logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelDebug,
}))

// Monitor metrics
// Check livestatus_actor_* metrics in Prometheus/Grafana
```

## Contributing

When adding new features:

1. **Add tests** - Ensure comprehensive test coverage
2. **Update documentation** - Keep README.md current
3. **Follow patterns** - Use existing patterns for consistency
4. **Security review** - Ensure no injection vulnerabilities

## License

See the main project LICENSE file for details.
