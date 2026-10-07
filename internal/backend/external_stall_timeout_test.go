package backend

import (
	"testing"
	"time"
)

// TestEnvDuration covers the HUGINN_EXTERNAL_STALL_TIMEOUT_SECS parsing
// contract: bare seconds and Go duration strings both override the default, and
// every invalid form falls back to it rather than producing a zero or negative
// timeout (which would abort every stream instantly).
//
// Duration strings are covered deliberately: the originating issue suggested
// "180s", so that spelling must work rather than silently no-op.
func TestEnvDuration(t *testing.T) {
	const def = 60 * time.Second

	tests := []struct {
		name string
		set  bool
		val  string
		want time.Duration
	}{
		{name: "unset uses default", set: false, want: def},
		{name: "empty uses default", set: true, val: "", want: def},
		{name: "whitespace uses default", set: true, val: "   ", want: def},
		{name: "valid override", set: true, val: "300", want: 300 * time.Second},
		{name: "valid override with surrounding space", set: true, val: " 120 ", want: 120 * time.Second},
		{name: "one second is allowed", set: true, val: "1", want: 1 * time.Second},
		{name: "zero falls back", set: true, val: "0", want: def},
		{name: "negative falls back", set: true, val: "-5", want: def},
		{name: "non-numeric falls back", set: true, val: "abc", want: def},
		{name: "float falls back", set: true, val: "1.5", want: def},

		// Duration-string forms — the spelling the originating issue suggested.
		{name: "duration string seconds", set: true, val: "180s", want: 180 * time.Second},
		{name: "duration string minutes", set: true, val: "5m", want: 5 * time.Minute},
		{name: "duration string mixed", set: true, val: "2m30s", want: 150 * time.Second},
		{name: "negative duration string falls back", set: true, val: "-30s", want: def},
		{name: "zero duration string falls back", set: true, val: "0s", want: def},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const key = "HUGINN_TEST_STALL_SECS"
			if tc.set {
				t.Setenv(key, tc.val)
			}
			if got := envDuration(key, def); got != tc.want {
				t.Errorf("envDuration(%q=%q) = %v, want %v", key, tc.val, got, tc.want)
			}
		})
	}
}

// TestExternalStreamStallTimeoutDefault guards the package-level default. The
// issue this configurability addresses was a 60 s timeout killing slow local
// prefill, so the shipped default must stay at 60 s unless deliberately changed.
func TestExternalStreamStallTimeoutDefault(t *testing.T) {
	if defaultExternalStreamStallTimeout != 60*time.Second {
		t.Errorf("default stall timeout = %v, want 60s", defaultExternalStreamStallTimeout)
	}
}
