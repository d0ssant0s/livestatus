package livestatus

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/matryer/is"
)

func TestQueryOneOffNilConfig(t *testing.T) {
	is := is.New(t)

	ctx := context.Background()
	_, err := QueryOneOff(ctx, "GET hosts", nil)
	is.True(err != nil)
	is.Equal(err.Error(), "config cannot be nil")
}

func TestQueryOneOffFromBuilderNilQuery(t *testing.T) {
	is := is.New(t)

	ctx := context.Background()
	config := NewLiveStatusConfig("localhost:6557")
	_, err := QueryOneOffFromBuilder(ctx, nil, config)
	is.True(err != nil)
	is.Equal(err.Error(), "query cannot be nil")
}

func TestQueryOneOffConnectionError(t *testing.T) {
	is := is.New(t)

	ctx := context.Background()
	config := NewLiveStatusConfig("nonexistent:6557")

	// Use a very short timeout to fail fast
	config.ConnectTimeout = 100 * time.Millisecond

	result, err := QueryOneOff(ctx, "GET hosts", config)
	is.NoErr(err) // QueryOneOff returns a Result with error info on failures
	is.Equal(result.StatusCode, 500)
	is.True(result.Error != nil)
	is.True(strings.Contains(result.Error.Error(), "failed to connect"))
}

func TestQueryOneOffFromBuilder(t *testing.T) {
	is := is.New(t)

	ctx := context.Background()
	query := NewLiveStatusQuery(Table("hosts"), "name", "state").OutputFormat(OutputJSON)
	config := NewLiveStatusConfig("localhost:6557")
	// Fail fast since no real livestatus server is likely available
	config.ConnectTimeout = 100 * time.Millisecond

	// This will likely fail due to no actual livestatus server, but exercises the function
	result, err := QueryOneOffFromBuilder(ctx, query, config)
	is.NoErr(err)
	is.True(result != nil)
}
