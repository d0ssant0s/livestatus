package livestatus

import "testing"

func TestParseFixed16Header_OK(t *testing.T) {
	h := []byte("200 00000000012\n")
	code, n, err := parseFixed16Header(h)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != StatusOK {
		t.Fatalf("code = %d, want %d", code, StatusOK)
	}
	if n != 12 {
		t.Fatalf("length = %d, want %d", n, 12)
	}
}

func TestParseFixed16Header_NotFound(t *testing.T) {
	h := []byte("404 00000000005\n")
	code, n, err := parseFixed16Header(h)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != StatusNotFound {
		t.Fatalf("code = %d, want %d", code, StatusNotFound)
	}
	if n != 5 {
		t.Fatalf("length = %d, want %d", n, 5)
	}
}

func TestParseFixed16Header_BadLen(t *testing.T) {
	h := []byte("200 0000000001") // 15 bytes, missing trailing \n
	if _, _, err := parseFixed16Header(h); err == nil {
		t.Fatalf("expected error for header len != 16, got nil")
	}
}

func TestParseFixed16Header_InvalidCode(t *testing.T) {
	h := []byte("2X0 0000000001\n")
	if _, _, err := parseFixed16Header(h); err == nil {
		t.Fatalf("expected error for invalid status code, got nil")
	}
}

func TestParseFixed16Header_InvalidLength(t *testing.T) {
	h := []byte("200 00000X001\n")
	if _, _, err := parseFixed16Header(h); err == nil {
		t.Fatalf("expected error for invalid length, got nil")
	}
}

func TestParseFixed16Header_WeirdSeparatorsButParsable(t *testing.T) {
	// Not a space at pos 3, not a newline at pos 15, but digits are in the right ranges.
	h := []byte{'2','0','0','X',' ','0','0','0','0','0','0','0','0','1','2','X'}
	// Adjust to still be 16 bytes; parser only cares about positions 0-2 and 4-14 being digits.
	code, n, err := parseFixed16Header(h)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != StatusOK || n != 12 {
		t.Fatalf("got code=%d len=%d; want 200 and 12", code, n)
	}
}
