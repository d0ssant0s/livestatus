package livestatus

import (
	"fmt"
	"slices"
	"strings"
)

// Table denotes a Livestatus table (e.g., "hosts", "services", "status", ...).
type Table string

// OutputFormat values accepted by Livestatus.
type OutputFormat string

const (
	OutputCSV  OutputFormat = "csv"
	OutputJSON OutputFormat = "json"
	OutputPY   OutputFormat = "python"
)

// Op enumerates filter operators. Not exhaustive; add as needed.
type Op string

const (
	OpEq   Op = "="
	OpNe   Op = "!="
	OpLt   Op = "<"
	OpLe   Op = "<="
	OpGt   Op = ">"
	OpGe   Op = ">="
	OpRe   Op = "~"  // regex
	OpReIC Op = "~~" // case-insensitive regex
	// List operators exist in Livestatus but we'll add helpers when needed.
)

// LiveStatusQuery builds a single LQL request.
type LiveStatusQuery struct {
	table        Table
	columns      []string
	filters      []string // each entry is a full "Filter: ..." line or And:/Or:/Negate:
	headers      []string // other headers (Limit, Wait*, ResponseHeader, KeepAlive, etc.)
	outputFormat OutputFormat
	columnHdrs   *bool // nil=unset; true/false -> ColumnHeaders: on/off
}

// NewLiveStatusQuery constructs a new builder.
func NewLiveStatusQuery(table Table, columns ...string) *LiveStatusQuery {
	// Defensive copy to avoid caller mutation.
	cpy := slices.Clone(columns)
	return &LiveStatusQuery{table: table, columns: cpy}
}

// Columns replaces the column list.
func (q *LiveStatusQuery) Columns(cols ...string) *LiveStatusQuery {
	q.columns = slices.Clone(cols)
	return q
}

// AddColumns appends to the column list.
func (q *LiveStatusQuery) AddColumns(cols ...string) *LiveStatusQuery {
	q.columns = append(q.columns, cols...)
	return q
}

// OutputFormat sets desired format (csv/json/python). Defaults to csv if unset.
func (q *LiveStatusQuery) OutputFormat(fmt OutputFormat) *LiveStatusQuery {
	q.outputFormat = fmt
	return q
}

// ColumnHeaders toggles "ColumnHeaders: on/off".
func (q *LiveStatusQuery) ColumnHeaders(on bool) *LiveStatusQuery {
	q.columnHdrs = &on
	return q
}

// Filter appends a generic filter: "Filter: column <op> value".
func (q *LiveStatusQuery) Filter(column string, op Op, value string) *LiveStatusQuery {
	q.filters = append(q.filters, fmt.Sprintf("Filter: %s %s %s", safeToken(column), op, safeValue(value)))
	return q
}

// Convenience helpers:

func (q *LiveStatusQuery) FilterEqual(column, value string) *LiveStatusQuery {
	return q.Filter(column, OpEq, value)
}
func (q *LiveStatusQuery) FilterNotEqual(column, value string) *LiveStatusQuery {
	return q.Filter(column, OpNe, value)
}
func (q *LiveStatusQuery) FilterLessThan(column, value string) *LiveStatusQuery {
	return q.Filter(column, OpLt, value)
}
func (q *LiveStatusQuery) FilterLessOrEqual(column, value string) *LiveStatusQuery {
	return q.Filter(column, OpLe, value)
}
func (q *LiveStatusQuery) FilterGreaterThan(column, value string) *LiveStatusQuery {
	return q.Filter(column, OpGt, value)
}
func (q *LiveStatusQuery) FilterGreaterOrEqual(column, value string) *LiveStatusQuery {
	return q.Filter(column, OpGe, value)
}
func (q *LiveStatusQuery) FilterRegex(column, pattern string) *LiveStatusQuery {
	return q.Filter(column, OpRe, pattern)
}
func (q *LiveStatusQuery) FilterRegexI(column, pattern string) *LiveStatusQuery {
	return q.Filter(column, OpReIC, pattern)
}

// Boolean composition (the order matters; these map 1:1 to LQL):
// After pushing N previous filters, calling Or(2) will add "Or: 2" to glue last two filters.
// Same for And(). Negate() flips the last filter or expression.
func (q *LiveStatusQuery) Or(n int) *LiveStatusQuery {
	q.filters = append(q.filters, fmt.Sprintf("Or: %d", n))
	return q
}
func (q *LiveStatusQuery) And(n int) *LiveStatusQuery {
	q.filters = append(q.filters, fmt.Sprintf("And: %d", n))
	return q
}
func (q *LiveStatusQuery) Negate() *LiveStatusQuery {
	q.filters = append(q.filters, "Negate:")
	return q
}

// Limit appends "Limit: N".
func (q *LiveStatusQuery) Limit(n int) *LiveStatusQuery {
	q.headers = append(q.headers, fmt.Sprintf("Limit: %d", n))
	return q
}

// Wait* helpers (optional long-polling patterns):
func (q *LiveStatusQuery) WaitObject(object string) *LiveStatusQuery {
	q.headers = append(q.headers, fmt.Sprintf("WaitObject: %s", safeValue(object)))
	return q
}
func (q *LiveStatusQuery) WaitTrigger(trigger string) *LiveStatusQuery {
	// allowed: check|state|log|downtime|comment|command|program|all
	q.headers = append(q.headers, fmt.Sprintf("WaitTrigger: %s", safeToken(trigger)))
	return q
}
func (q *LiveStatusQuery) WaitCondition(cond string) *LiveStatusQuery {
	q.headers = append(q.headers, fmt.Sprintf("WaitCondition: %s", safeValue(cond)))
	return q
}
func (q *LiveStatusQuery) WaitTimeout(ms int) *LiveStatusQuery {
	q.headers = append(q.headers, fmt.Sprintf("WaitTimeout: %d", ms))
	return q
}

// Connection/response framing:
func (q *LiveStatusQuery) KeepAlive(on bool) *LiveStatusQuery {
	if on {
		q.headers = append(q.headers, "KeepAlive: on")
	} else {
		q.headers = append(q.headers, "KeepAlive: off")
	}
	return q
}
func (q *LiveStatusQuery) ResponseHeaderFixed16() *LiveStatusQuery {
	q.headers = append(q.headers, "ResponseHeader: fixed16")
	return q
}
func (q *LiveStatusQuery) ResponseHeaderOff() *LiveStatusQuery {
	q.headers = append(q.headers, "ResponseHeader: off")
	return q
}

// Localtime header (unix seconds).
func (q *LiveStatusQuery) Localtime(ts int64) *LiveStatusQuery {
	q.headers = append(q.headers, fmt.Sprintf("Localtime: %d", ts))
	return q
}

// Stats*, Group*, and any advanced headers can be injected via Header():
func (q *LiveStatusQuery) Header(key, value string) *LiveStatusQuery {
	key = strings.TrimSpace(strings.TrimSuffix(key, ":"))
	if key == "" {
		return q
	}
	if value == "" {
		q.headers = append(q.headers, fmt.Sprintf("%s:", key))
		return q
	}
	q.headers = append(q.headers, fmt.Sprintf("%s: %s", key, safeValue(value)))
	return q
}

// Build assembles the final LQL request, ending with a blank line.
func (q *LiveStatusQuery) Build() string {
	var lines []string
	lines = append(lines, fmt.Sprintf("GET %s", q.table))
	if len(q.columns) > 0 {
		lines = append(lines, "Columns: "+strings.Join(safeTokens(q.columns), " "))
	}
	lines = append(lines, q.filters...)
	// ColumnHeaders must appear before OutputFormat ideally, but order is not strict.
	if q.columnHdrs != nil {
		if *q.columnHdrs {
			lines = append(lines, "ColumnHeaders: on")
		} else {
			lines = append(lines, "ColumnHeaders: off")
		}
	}
	lines = append(lines, q.headers...)
	if q.outputFormat != "" {
		lines = append(lines, "OutputFormat: "+string(q.outputFormat))
	}
	//terminate request
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

// --- helpers ---

// safeToken removes CR/LF and trims; tokens should not contain spaces in headers like keys or triggers.
func safeToken(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

func safeTokens(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, safeToken(v))
	}
	return out
}

// safeValue: Livestatus does not use quoting; values are raw. We prevent header injection by stripping CR/LF.
// Spaces are allowed for many headers (e.g., WaitCondition regex); we keep them.
func safeValue(s string) string {
	s = strings.ReplaceAll(s, "\x00", "") // guard against NUL
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s // Don't trim spaces, keep them as-is
}
