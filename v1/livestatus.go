package livestatus

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// ---------- Lightweight ID machinery ----------

// RequestID identifies a submitted work item.
type RequestID = uint64

var idCounter atomic.Uint64

func init() {
	// Seed the counter with a non-zero, time-based value. This is purely to reduce
	// collision chances across short-lived processes and tests; strict uniqueness
	// guarantees are out of scope and not required here.
	seed := uint64(time.Now().UnixNano())
	if seed == 0 {
		seed = 1
	}
	idCounter.Store(seed)
}

// nextRequestID returns a new, monotonically increasing ID. Cheap & lock-free.
func nextRequestID() RequestID {
	return idCounter.Add(1)
}

// Common LiveStatus status codes returned in the fixed16 header.
// This is not an exhaustive list, just the most commonly observed ones.
const (
	StatusOK                  = 200
	StatusBadRequest          = 400
	StatusUnauthorized        = 401
	StatusForbidden           = 403
	StatusNotFound            = 404
	StatusInternalServerError = 500
	StatusServiceUnavailable  = 503
)

// statusText holds human-friendly descriptions for common codes.
var statusText = map[int]string{
	StatusOK:                  "OK",
	StatusBadRequest:          "Bad Request",
	StatusUnauthorized:        "Unauthorized",
	StatusForbidden:           "Forbidden",
	StatusNotFound:            "Not Found",
	StatusInternalServerError: "Internal Server Error",
	StatusServiceUnavailable:  "Service Unavailable",
}

// StatusText returns a short text for the given status code.
// If the code is unknown, it returns "".
func StatusText(code int) string {
	if s, ok := statusText[code]; ok {
		return s
	}
	return "Unknown"
}

// ---------- Types shared with the outside ----------

// Result represents the result of processing a work item
type Result struct {
	StatusCode int
	Data       []byte
	Error      error
}

// ResultMsg is what the actor publishes to the shared results bus.
type ResultMsg struct {
	ID     RequestID
	Result *Result
}

// WorkItem represents a work item to be processed by the actor
// (no per-request channel anymore).
type WorkItem struct {
	ID    RequestID
	Query LiveStatusQuery
}

// LiveStatusConfig holds configuration for direct livestatus connections
type LiveStatusConfig struct {
	// Connection details
	Address string // TCP address (e.g., "localhost:6557") or Unix socket path

	// Optional settings
	ConnectTimeout time.Duration // Time to wait for connection (default: 10s)
	ReadTimeout    time.Duration // Time to wait for response (default: 30s)
	WriteTimeout   time.Duration // Time to wait for write (default: 30s)
	// Safety cap for response body to avoid unbounded allocations (default: 32 MiB)
	MaxBodyBytes int64

	// TLS settings
	// UseTLS enables TLS when connecting over TCP. Ignored for Unix sockets.
	UseTLS bool
	// InsecureSkipVerify skips server certificate verification. DO NOT use in production.
	InsecureSkipVerify bool
	// CAFile is an optional path to a PEM-encoded CA bundle to trust in addition to system roots.
	CAFile string
	// CertFile and KeyFile optionally provide a client certificate for mTLS.
	CertFile string
	KeyFile  string
}

// NewLiveStatusConfig creates a new configuration with sensible defaults.
func NewLiveStatusConfig(address string) *LiveStatusConfig {
	return &LiveStatusConfig{
		Address:        address,
		ConnectTimeout: 10 * time.Second,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   30 * time.Second,
		MaxBodyBytes:   32 << 20,
	}
}

// NewWorkItemFromQuery converts a built query into a WorkItem with the given ID.
func NewWorkItemFromQuery(id RequestID, q *LiveStatusQuery) *WorkItem {
	return &WorkItem{
		ID:    id,
		Query: *q, // store a value copy; builder remains editable by caller afterwards
	}
}

// ---------- Low-level Livestatus I/O (unchanged) ----------

func connectToLiveStatus(ctx context.Context, cfg *LiveStatusConfig) (net.Conn, error) {
	// Determine if it's a Unix socket or TCP connection
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
	}

	// Check if address looks like a Unix socket path (contains no colon)
	if !strings.Contains(cfg.Address, ":") {
		// Unix socket
		return dialer.DialContext(ctx, "unix", cfg.Address)
	}

	// TCP connection
	if cfg != nil && cfg.UseTLS {
		tlsConf, err := buildTLSConfig(cfg)
		if err != nil {
			return nil, err
		}
		td := &tls.Dialer{NetDialer: dialer, Config: tlsConf}
		return td.DialContext(ctx, "tcp", cfg.Address)
	}
	return dialer.DialContext(ctx, "tcp", cfg.Address)
}

func buildTLSConfig(cfg *LiveStatusConfig) (*tls.Config, error) {
	tlsConf := &tls.Config{
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	}

	if cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA file")
		}
		tlsConf.RootCAs = caCertPool
	}

	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConf.Certificates = []tls.Certificate{cert}
	}

	return tlsConf, nil
}

func sendQuery(conn net.Conn, query string) error {
	// Ensure query ends with a blank line (livestatus protocol requirement)
	if !strings.HasSuffix(query, "\n\n") {
		if !strings.HasSuffix(query, "\n") {
			query += "\n"
		}
		query += "\n"
	}

	// Send the query
	_, err := conn.Write([]byte(query))
	return err
}

func readResponse(conn net.Conn) ([]byte, error) {
	var response []byte
	reader := bufio.NewReader(conn)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				// EOF is expected at end of response
				response = append(response, line...)
				break
			}
			return nil, fmt.Errorf("error reading response: %w", err)
		}

		response = append(response, line...)

		// Check if we've reached the end of the response (empty line)
		if len(line) == 1 && line[0] == '\n' {
			// We found an empty line, which typically indicates end of response
			// But we need to check if there's more data after it
			if reader.Buffered() == 0 {
				// No more buffered data, this is likely the end
				break
			}
		}
	}

	return response, nil
}
