package livestatus

import (
	"encoding/json"
	"fmt"
	"time"
)

// ConnectivityState mirrors a simple connection lifecycle.
type ConnectivityState int

const (
	StateUnknown ConnectivityState = iota
	StateConnected
	StateDisconnected
	StateRetrying
	StateShutdown
)

// ConnectivityEvent describes a connection lifecycle update.
type ConnectivityEvent struct {
	Actor   string
	State   ConnectivityState
	Time    time.Time
	Reason  string
	Attempt int
	Backoff time.Duration
	Err     error
}

// String returns a human-friendly name for the connectivity state.
func (s ConnectivityState) String() string {
	switch s {
	case StateConnected:
		return "connected"
	case StateDisconnected:
		return "disconnected"
	case StateRetrying:
		return "retrying"
	case StateShutdown:
		return "shutdown"
	default:
		return fmt.Sprintf("unknown_state_%d", int(s))
	}
}

// connectivityEventJSON is a helper struct for JSON serialization of ConnectivityEvent
// to keep the wire format stable and user-friendly.
type connectivityEventJSON struct {
	Actor   string    `json:"actor,omitempty"`
	State   string    `json:"state"`
	Time    time.Time `json:"time"`
	Reason  string    `json:"reason,omitempty"`
	Attempt int       `json:"attempt,omitempty"`
	Error   string    `json:"error,omitempty"`
	Backoff string    `json:"backoff,omitempty"`
}

// ToJSON returns a JSON string representation of the ConnectivityEvent for debugging purposes.
// It returns a formatted JSON string and never returns an error.
func (e ConnectivityEvent) ToJSON() string {
	ce := connectivityEventJSON{
		Actor:   e.Actor,
		State:   e.State.String(),
		Time:    e.Time,
		Reason:  e.Reason,
		Attempt: e.Attempt,
	}
	if e.Err != nil {
		ce.Error = e.Err.Error()
	}
	if e.Backoff > 0 {
		ce.Backoff = e.Backoff.String()
	}
	b, err := json.MarshalIndent(ce, "", "  ")
	if err != nil {
		return fmt.Sprintf("{\"state\":%q,\"error\":%q}", e.State.String(), err.Error())
	}
	return string(b)
}
