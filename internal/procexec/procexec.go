// Package procexec holds the process-execution mechanics shared by the sandboxed
// server and the local stdio helper: tearing down a command's whole process tree,
// and capturing bounded output without cutting a character in half.
//
// It exists so the two subtle parts have exactly one implementation each. The
// group kill has to converge on processes forked while the signal was being
// delivered, and output has to be cut on a character boundary; duplicating either
// would mean fixing it twice and noticing only in whichever binary someone
// happened to test.
package procexec

// CappedBuffer accumulates up to a byte limit and reports whether anything was
// dropped.
//
// Writes past the limit are reported as accepted so the producer keeps running:
// the goal is to bound what is returned to a caller, not to break the command's
// stdout with a short write.
type CappedBuffer struct {
	buf       []byte
	limit     int
	truncated bool
}

// NewCappedBuffer returns a buffer holding at most limit bytes.
func NewCappedBuffer(limit int) *CappedBuffer {
	return &CappedBuffer{limit: limit}
}

func (b *CappedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - len(b.buf)
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.buf = append(b.buf, p[:remaining]...)
		b.truncated = true
		return len(p), nil
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

// Truncated reports whether the limit dropped any output.
func (b *CappedBuffer) Truncated() bool { return b.truncated }

// String returns the accumulated output, dropping a character the limit cut in
// half.
func (b *CappedBuffer) String() string {
	if b.truncated {
		return string(TrimIncompleteRune(b.buf))
	}
	return string(b.buf)
}
