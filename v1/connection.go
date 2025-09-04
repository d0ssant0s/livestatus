package livestatus

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"
)

// ioReadFull mirrors io.ReadFull but uses the actor's buffered reader semantics consistently.
func ioReadFull(r *bufio.Reader, buf []byte) (int, error) {
	read := 0
	for read < len(buf) {
		n, err := r.Read(buf[read:])
		read += n
		if err != nil {
			return read, err
		}
	}
	return read, nil
}

// parseFixed16Header parses the 16-byte fixed16 header.
// Layout:
//
//	bytes 0-2: status code (ASCII digits)
//	byte 3: space (ASCII 32)
//	bytes 4-14: payload length as ASCII int, left-padded by spaces
//	byte 15: newline (ASCII 10)
func parseFixed16Header(hdr []byte) (code int, length int, err error) {
	if len(hdr) != 16 {
		return 0, 0, fmt.Errorf("fixed16 header must be 16 bytes, got %d", len(hdr))
	}
	// Best-effort validation
	if hdr[3] != ' ' || hdr[15] != '\n' {
		// Continue parsing, but report a descriptive error if needed later
	}
	codeStr := strings.TrimSpace(string(hdr[0:3]))
	code, err = strconv.Atoi(codeStr)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid status code in header %q: %w", string(hdr), err)
	}
	lenStr := strings.TrimSpace(string(hdr[4:15]))
	length, err = strconv.Atoi(lenStr)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid length in header %q: %w", string(hdr), err)
	}
	return code, length, nil
}

// ensureConn ensures there is a live connection and reader, dialing if necessary.
func ensureConn(ctx context.Context, cfg *LiveStatusConfig, conn net.Conn) (net.Conn, *bufio.Reader, error) {
	if conn != nil {
		// Probe existing connection liveness. We set an immediate read deadline and
		// attempt a non-consuming Peek on a temporary reader. If it returns EOF or
		// a non-timeout network error, consider the connection dead and redial.
		_ = conn.SetReadDeadline(time.Now())
		tmp := bufio.NewReader(conn)
		if _, err := tmp.Peek(1); err != nil {
			// If it's a timeout, the conn is likely idle but alive; keep it.
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				// clear deadline and continue using this connection
				clearDeadlines(conn)
				return conn, tmp, nil
			}
			// EOF or other error -> treat as dead and drop through to redial
		} else {
			// Data available; clear deadline and use this connection with a fresh reader
			clearDeadlines(conn)
			return conn, tmp, nil
		}
		// Clear deadlines before redialing
		clearDeadlines(conn)
	}
	if cfg == nil {
		return nil, nil, fmt.Errorf("no config for livestatus connection")
	}
	// Use configured ConnectTimeout if present
	dctx := ctx
	if cfg.ConnectTimeout > 0 {
		var cancel context.CancelFunc
		dctx, cancel = context.WithTimeout(ctx, cfg.ConnectTimeout)
		defer cancel()
	}
	c, err := connectToLiveStatus(dctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to livestatus: %w", err)
	}
	return c, bufio.NewReader(c), nil
}

// execOverPersistentConn sends a single query with required headers and reads the fixed16 response.
func execOverPersistentConn(logger *slog.Logger, ctx context.Context, cfg *LiveStatusConfig, conn net.Conn, reader *bufio.Reader, query LiveStatusQuery) (*Result, error) {
	if conn == nil || reader == nil {
		return nil, fmt.Errorf("nil connection/reader")
	}
	// Build an effective query enforcing required headers without parsing strings.
	q := query // work on a local copy
	q.ResponseHeaderFixed16().KeepAlive(true)
	queryStr := q.Build()
	// Ensure request ends with a blank line per livestatus protocol
	if !strings.HasSuffix(queryStr, "\n\n") {
		if !strings.HasSuffix(queryStr, "\n") {
			queryStr += "\n"
		}
		queryStr += "\n"
	}
	logger.Debug("execOverPersistentConn", "query", queryStr)

	// Derive deadlines: prefer ctx.Deadline if earlier; otherwise use cfg timeouts
	// Write deadline first (use WriteTimeout when provided)
	if cfg != nil && cfg.WriteTimeout > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(cfg.WriteTimeout))
	} else if cfg != nil && cfg.ReadTimeout > 0 {
		// Fallback to ReadTimeout if WriteTimeout is not set
		_ = conn.SetWriteDeadline(time.Now().Add(cfg.ReadTimeout))
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(dl)
	}
	if err := writeAll(conn, queryStr); err != nil {
		clearDeadlines(conn)
		return nil, fmt.Errorf("write failed: %w", err)
	}
	// Then read deadline
	if cfg != nil && cfg.ReadTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(cfg.ReadTimeout))
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(dl)
	}
	defer clearDeadlines(conn)

	// Allow early cancel between phases
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Read fixed16 header
	var hdr [16]byte
	if _, err := ioReadFull(reader, hdr[:]); err != nil {
		logger.Warn("execOverPersistentConn", "err", err, "query", queryStr, "hdr", fmt.Sprintf("%x", hdr))
		return nil, fmt.Errorf("failed reading fixed16 header: %w", err)
	}
	code, n, err := parseFixed16Header(hdr[:])
	if err != nil {
		return nil, fmt.Errorf("invalid fixed16 header: %w", err)
	}

	// Guard allocations based on configured cap (default 32 MiB)
	maxBody := 32 << 20
	if cfg != nil && cfg.MaxBodyBytes > 0 {
		maxBody = int(cfg.MaxBodyBytes)
	}
	if n < 0 || n > maxBody {
		// Best-effort drain to keep connection reusable
		if err := discardN(reader, int64(n)); err != nil {
			return nil, fmt.Errorf("response too large (%d bytes) and drain failed: %v", n, err)
		}
		return nil, fmt.Errorf("response too large: %d bytes (cap %d)", n, maxBody)
	}

	body := make([]byte, n)
	if _, err := ioReadFull(reader, body); err != nil {
		return nil, fmt.Errorf("failed reading body: %w", err)
	}

	if code != StatusOK {
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = "livestatus error"
		}
		return &Result{StatusCode: code, Error: fmt.Errorf("livestatus status %d: %s", code, msg)}, nil
	}
	return &Result{StatusCode: code, Data: body}, nil
}

// writeAll handles short writes by looping until all bytes are written.
func writeAll(w net.Conn, s string) error {
	b := []byte(s)
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}

func clearDeadlines(conn net.Conn) {
	_ = conn.SetDeadline(time.Time{})
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})
}

// discardN drains exactly n bytes from r, returning an error if it can't.
func discardN(r io.Reader, n int64) error {
	if n <= 0 {
		return nil
	}
	_, err := io.CopyN(io.Discard, r, n)
	return err
}
