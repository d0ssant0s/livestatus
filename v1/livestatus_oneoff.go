package livestatus

import (
	"context"
	"fmt"
	"time"
)

// QueryOneOff executes a livestatus query directly without using the actor.
// This connects to the livestatus service, runs the query, and returns the result.
func QueryOneOff(ctx context.Context, query string, config *LiveStatusConfig) (*Result, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Create connection timeout context
	connectCtx, cancel := context.WithTimeout(ctx, config.ConnectTimeout)
	defer cancel()

	// Connect to livestatus
	conn, err := connectToLiveStatus(connectCtx, config)
	if err != nil {
		return &Result{
			StatusCode: 500,
			Error:      fmt.Errorf("failed to connect to livestatus: %w", err),
		}, nil
	}
	defer conn.Close()

	// Set read timeout
	if config.ReadTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(config.ReadTimeout))
	}

	// Send query
	if err := sendQuery(conn, query); err != nil {
		return &Result{
			StatusCode: 500,
			Error:      fmt.Errorf("failed to send query: %w", err),
		}, nil
	}

	// Read response
	data, err := readResponse(conn)
	if err != nil {
		return &Result{
			StatusCode: 500,
			Error:      fmt.Errorf("failed to read response: %w", err),
		}, nil
	}

	return &Result{
		StatusCode: 200,
		Data:       data,
	}, nil
}

// QueryOneOffFromBuilder executes a query built with LiveStatusQuery directly.
func QueryOneOffFromBuilder(ctx context.Context, query *LiveStatusQuery, config *LiveStatusConfig) (*Result, error) {
	if query == nil {
		return nil, fmt.Errorf("query cannot be nil")
	}
	return QueryOneOff(ctx, query.Build(), config)
}
