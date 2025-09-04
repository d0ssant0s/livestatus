package livestatus

import (
	"strings"
	"testing"

	"github.com/matryer/is"
)

func TestBasicBuild(t *testing.T) {
	is := is.New(t)

	q := NewLiveStatusQuery(Table("hosts"), "name", "state").
		OutputFormat(OutputJSON).
		Limit(10)

	got := q.Build()
	want := "GET hosts\nColumns: name state\nLimit: 10\nOutputFormat: json\n"

	is.Equal(got, want)
}

func TestFiltersAndBoolGlue(t *testing.T) {
	is := is.New(t)

	q := NewLiveStatusQuery(Table("services"), "host_name", "description", "state").
		FilterEqual("state", "2").
		FilterEqual("acknowledged", "0").
		And(2). // state=2 AND ack=0
		FilterRegexI("description", "http.*backend").
		Or(2). // (previous AND) OR regex
		OutputFormat(OutputCSV)

	got := q.Build()
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")

	// Order checks
	is.Equal(lines[0], "GET services")
	is.Equal(lines[1], "Columns: host_name description state")
	is.Equal(lines[2], "Filter: state = 2")
	is.Equal(lines[3], "Filter: acknowledged = 0")
	is.Equal(lines[4], "And: 2")
	is.Equal(lines[5], "Filter: description ~~ http.*backend")
	is.Equal(lines[6], "Or: 2")
	is.Equal(lines[7], "OutputFormat: csv")
}

func TestColumnHeadersAndKeepAlive(t *testing.T) {
	is := is.New(t)

	q := NewLiveStatusQuery(Table("hosts"), "name").
		ColumnHeaders(true).
		ResponseHeaderFixed16().
		KeepAlive(true).
		OutputFormat(OutputJSON)

	got := q.Build()
	want := "GET hosts\nColumns: name\nColumnHeaders: on\nResponseHeader: fixed16\nKeepAlive: on\nOutputFormat: json\n"

	is.Equal(got, want)
}

func TestWaitHelpers(t *testing.T) {
	is := is.New(t)

	q := NewLiveStatusQuery(Table("status")).
		WaitTrigger("log").
		WaitObject("host;my host with spaces").
		WaitCondition("class = 3").
		WaitTimeout(5000)

	got := q.Build()
	wantLines := []string{
		"GET status",
		"WaitTrigger: log",
		"WaitObject: host;my host with spaces",
		"WaitCondition: class = 3",
		"WaitTimeout: 5000",
		"",
	}
	is.Equal(strings.Split(got, "\n"), wantLines)
}

func TestHeaderInjectionAndSanitization(t *testing.T) {
	is := is.New(t)

	q := NewLiveStatusQuery(Table("services"), "host_name").
		FilterEqual("host_name", "bad\r\nname"). // CRLF should be stripped
		Header("Stats:    ", "state = 2").
		Header("Localtime", "1724000000\n").
		Header("WeirdEmpty", "").
		OutputFormat(OutputCSV)

	got := q.Build()
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")

	is.Equal(lines[0], "GET services")
	is.Equal(lines[1], "Columns: host_name")
	is.Equal(lines[2], "Filter: host_name = bad  name")
	is.Equal(lines[3], "Stats:: state = 2")
	is.Equal(lines[4], "Localtime: 1724000000 ")
	is.Equal(lines[5], "WeirdEmpty:")
	is.Equal(lines[6], "OutputFormat: csv")
}
