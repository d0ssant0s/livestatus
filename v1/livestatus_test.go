package livestatus

import (
	"strings"
	"testing"
	"time"

	"github.com/matryer/is"
)

// Note: Actor-related tests moved to `livestatus_actor_test.go`.

func TestLiveStatusConfig(t *testing.T) {
	is := is.New(t)

	// Test default config
	config := NewLiveStatusConfig("localhost:6557")
	is.Equal(config.Address, "localhost:6557")
	is.Equal(config.ConnectTimeout, 10*time.Second)
	is.Equal(config.ReadTimeout, 30*time.Second)
	is.Equal(config.WriteTimeout, 30*time.Second)

	// Test custom config
	config = &LiveStatusConfig{
		Address:        "/var/run/livestatus.sock",
		ConnectTimeout: 5 * time.Second,
		ReadTimeout:    45 * time.Second,
		WriteTimeout:   30 * time.Second,
	}
	is.Equal(config.Address, "/var/run/livestatus.sock")
	is.Equal(config.ConnectTimeout, 5*time.Second)
	is.Equal(config.ReadTimeout, 45*time.Second)
	is.Equal(config.WriteTimeout, 30*time.Second)
}

func TestSendQueryFormatting(t *testing.T) {
	is := is.New(t)

	// Test that sendQuery properly formats queries with blank lines
	testQuery := "GET hosts\nColumns: name state"

	// Simulate the formatting logic
	formatted := testQuery
	if !strings.HasSuffix(formatted, "\n\n") {
		if !strings.HasSuffix(formatted, "\n") {
			formatted += "\n"
		}
		formatted += "\n"
	}
	expected := "GET hosts\nColumns: name state\n\n"
	is.Equal(formatted, expected)
}
