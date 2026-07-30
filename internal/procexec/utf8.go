package procexec

// Output is read in fixed-size chunks, so a multi-byte character routinely
// straddles two reads. Publishing the halves as-is would put invalid UTF-8 into
// the event stream, which the JSON encoder replaces with U+FFFD — so a terminal
// watching a command that prints anything non-ASCII would show a replacement
// character at every chunk boundary, at a random position that moves with the
// output. The split is held back instead until the rest of the character
// arrives.

// runeByteLen returns how many bytes the UTF-8 character starting with b
// occupies, or 1 for a continuation or invalid start byte (which cannot begin a
// multi-byte character and so is never held back).
func runeByteLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b&0xE0 == 0xC0:
		return 2
	case b&0xF0 == 0xE0:
		return 3
	case b&0xF8 == 0xF0:
		return 4
	default:
		return 1
	}
}

// SplitCompleteRunes splits b after its last complete UTF-8 character, returning
// the complete prefix and any truncated character at the tail.
func SplitCompleteRunes(b []byte) (complete, carry []byte) {
	// A character is at most 4 bytes, so only the last 4 can be incomplete.
	start := max(len(b)-4, 0)

	for i := len(b) - 1; i >= start; i-- {
		if b[i] >= 0x80 && b[i] < 0xC0 {
			continue // continuation byte; keep scanning back for the start
		}
		if i+runeByteLen(b[i]) > len(b) {
			return b[:i], b[i:]
		}
		return b, nil
	}
	// Either b is empty, or its last 4 bytes are all continuation bytes — which
	// is malformed input, not a split character, and passing it through is more
	// honest than holding it forever.
	return b, nil
}

// TrimIncompleteRune drops a truncated character from the end of b, for output
// that was cut by a byte limit rather than by a chunk boundary. Postgres rejects
// invalid UTF-8 outright and JSON silently mangles it; neither is worth the one
// partial character.
func TrimIncompleteRune(b []byte) []byte {
	complete, _ := SplitCompleteRunes(b)
	return complete
}
