//go:build helm_testclock

package store

import (
	"testing"
	"time"
)

// The helm_testclock build rejects malformed and future instants
// (go test -tags helm_testclock ./internal/store; written after the code).
func TestTaskActivityTestClockParsing(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	if at, err := parseTestEvaluationTime("2026-11-08T13:00:00+02:00", now); err != nil || !at.Equal(now.Add(-time.Hour)) || at.Location() != time.UTC {
		t.Fatalf("parse = %s, %v", at, err)
	}
	for _, value := range []string{"yesterday", "2026-11-08T12:00:01Z"} {
		if _, err := parseTestEvaluationTime(value, now); err == nil {
			t.Fatalf("%q accepted", value)
		}
	}
}
