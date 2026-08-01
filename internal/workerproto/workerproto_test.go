package workerproto

import (
	"fmt"
	"testing"
)

// The tests here are about the relationships between the limits rather than
// about any one of their values.
//
// Sizes accumulate in a system like this — an output cap, a file cap, a frame,
// an envelope, a bootstrap — and the failure they invite is not a wrong number
// but a pair of numbers that no longer fit each other, changed months apart in
// different files. Each test below is one such pair, asserted over a spread of
// configurations rather than at the defaults, so a change that only works for
// today's values fails here.

// configurations covers the plausible range an operator might set, from a
// deliberately tiny worker to one near the ceiling.
func configurations() []Limits {
	var out []Limits
	for _, output := range []int{1 << 10, 64 << 10, 512 << 10, 4 << 20} {
		for _, file := range []int{1 << 10, 1 << 20, 8 << 20, 16 << 20} {
			out = append(out, Limits{MaxOutputBytes: output, MaxFileBytes: file})
		}
	}
	return out
}

func (l Limits) String() string {
	return fmt.Sprintf("output=%d file=%d", l.MaxOutputBytes, l.MaxFileBytes)
}

func TestAFrameCanCarryWhateverTheLimitsAllow(t *testing.T) {
	for _, l := range configurations() {
		if err := l.Validate(); err != nil {
			continue
		}

		// The point of deriving the frame from the caps: whatever the caps permit
		// has somewhere to go. A configuration that passes validation and then
		// cannot return its own maximum output would be the exact trap this
		// arithmetic exists to avoid.
		if got, want := l.MaxPayloadBytes(), 2*l.MaxOutputBytes; got < want {
			t.Errorf("%s: payload room %d, under the %d bytes stdout and stderr can be", l, got, want)
		}
		if got, want := l.MaxPayloadBytes(), l.MaxFileBytes; got < want {
			t.Errorf("%s: payload room %d, under the %d-byte file cap", l, got, want)
		}
	}
}

func TestEveryAcceptedConfigurationStaysUnderTheCeiling(t *testing.T) {
	for _, l := range configurations() {
		if err := l.Validate(); err != nil {
			continue
		}
		if got := l.FrameBytes(); got > MaxNegotiatedFrameBytes {
			t.Errorf("%s: validation accepted limits needing %d bytes, over the %d-byte ceiling",
				l, got, MaxNegotiatedFrameBytes)
		}
	}
}

func TestTheBootstrapLimitIsSmallerThanAnyRealFrame(t *testing.T) {
	// The server reads the hello under the bootstrap limit and then raises it. If
	// a frame could be smaller, that raise would be a shrink and the sequence
	// would be backwards without anything saying so.
	for _, l := range configurations() {
		if err := l.Validate(); err != nil {
			continue
		}
		if got := l.FrameBytes(); got <= HelloFrameBytes {
			t.Errorf("%s: frame is %d bytes, not above the %d-byte bootstrap limit", l, got, HelloFrameBytes)
		}
	}
}

func TestAHelloFitsTheBootstrapLimit(t *testing.T) {
	// The frame the bootstrap limit exists for has to fit under it, with room for
	// a worker id longer than the ones in these tests.
	frame := Frame{
		Type:     FrameHello,
		WorkerID: "executor-worker-7f9c4b2a1d8e-a-rather-long-pod-name-from-a-statefulset",
		Version:  "v1.2.3-rc4+build.20260801.abcdef0",
		Limits:   &Limits{MaxOutputBytes: 512 << 10, MaxFileBytes: 8 << 20},
	}
	raw, err := Marshal(frame)
	if err != nil {
		t.Fatalf("marshal hello: %v", err)
	}
	if len(raw) > HelloFrameBytes {
		t.Errorf("a hello encodes to %d bytes, over the %d-byte bootstrap limit", len(raw), HelloFrameBytes)
	}
}

func TestValidateRejectsWhatCannotWork(t *testing.T) {
	for name, l := range map[string]Limits{
		"no output at all": {MaxOutputBytes: 0, MaxFileBytes: 1 << 20},
		"negative output":  {MaxOutputBytes: -1, MaxFileBytes: 1 << 20},
		"no files at all":  {MaxOutputBytes: 1 << 10, MaxFileBytes: 0},
		"output past ceiling": {
			MaxOutputBytes: MaxNegotiatedFrameBytes,
			MaxFileBytes:   1 << 20,
		},
		"files past ceiling": {
			MaxOutputBytes: 1 << 10,
			MaxFileBytes:   MaxNegotiatedFrameBytes,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := l.Validate(); err == nil {
				t.Errorf("Validate accepted %s", l)
			}
		})
	}
}

func TestTheDefaultsProduceTheFrameTheLinkUsedBefore(t *testing.T) {
	// The shipped configuration is what every deployment starts from, and it used
	// to be a hard-coded 16MB socket. Deriving the number should not have quietly
	// moved it.
	defaults := Limits{MaxOutputBytes: 512 << 10, MaxFileBytes: 8 << 20}
	if err := defaults.Validate(); err != nil {
		t.Fatalf("the defaults do not validate: %v", err)
	}

	const was = 16 << 20
	if got := defaults.FrameBytes(); got != was+envelopeBytes {
		t.Errorf("defaults give a %d-byte frame, want the %d bytes the link used to hard-code plus its envelope",
			got, was)
	}
}
