package procexec

import "testing"

func TestSplitCompleteRunes(t *testing.T) {
	// "☃" is e2 98 83.
	tests := []struct {
		name         string
		in           string
		wantComplete string
		wantCarry    []byte
	}{
		{name: "empty", in: "", wantComplete: ""},
		{name: "ascii only", in: "hello", wantComplete: "hello"},
		{name: "whole multi-byte rune", in: "a☃", wantComplete: "a☃"},
		{
			name:         "rune cut after one byte",
			in:           "a\xe2",
			wantComplete: "a",
			wantCarry:    []byte{0xe2},
		},
		{
			name:         "rune cut after two bytes",
			in:           "a\xe2\x98",
			wantComplete: "a",
			wantCarry:    []byte{0xe2, 0x98},
		},
		{
			// A malformed tail is passed through rather than held forever: the
			// process may simply have written invalid bytes, and waiting for a
			// continuation that never comes would stall the terminal.
			name:         "malformed tail passes through",
			in:           "a\x98\x98\x98\x98",
			wantComplete: "a\x98\x98\x98\x98",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			complete, carry := SplitCompleteRunes([]byte(tt.in))
			if string(complete) != tt.wantComplete {
				t.Errorf("complete = %q, want %q", complete, tt.wantComplete)
			}
			if string(carry) != string(tt.wantCarry) {
				t.Errorf("carry = %v, want %v", carry, tt.wantCarry)
			}
		})
	}
}

func TestTruncatedOutputDropsAPartialRune(t *testing.T) {
	// "☃" is three bytes, so a 3-byte cap on "a☃" lands after its first two —
	// mid-character. Left alone that puts invalid UTF-8 in the tool result, which
	// JSON mangles and Postgres rejects outright. A cap that happens to land on a
	// boundary keeps the whole character; see the case below.
	buf := NewCappedBuffer(3)
	if _, err := buf.Write([]byte("a☃☃")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if !buf.Truncated() {
		t.Fatal("buffer is not marked truncated")
	}
	if got := buf.String(); got != "a" {
		t.Errorf("String() = %q, want %q — the partial rune should be dropped", got, "a")
	}
}

func TestUntruncatedOutputIsReturnedWhole(t *testing.T) {
	buf := NewCappedBuffer(1024)
	if _, err := buf.Write([]byte("a☃b")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := buf.String(); got != "a☃b" {
		t.Errorf("String() = %q, want %q", got, "a☃b")
	}
}

func TestTruncationOnARuneBoundaryKeepsTheCharacter(t *testing.T) {
	// A cap that happens to fall exactly after a character must not drop it:
	// trimming unconditionally would silently shorten every truncated result by
	// one character.
	buf := NewCappedBuffer(4)
	if _, err := buf.Write([]byte("a☃☃")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !buf.Truncated() {
		t.Fatal("buffer is not marked truncated")
	}
	if got := buf.String(); got != "a☃" {
		t.Errorf("String() = %q, want %q", got, "a☃")
	}
}
