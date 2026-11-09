//go:build helm_testclock

package store

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// Test builds only (go build -tags helm_testclock; make neglect-binary). Fixes
// the neglect-listing evaluation instant from HELM_TEST_EVALUATION_TIME so
// real-flow case 14 can place a card exactly at the threshold. Default builds
// do not compile this file. A malformed or future instant stops the process.
func init() {
	value := os.Getenv("HELM_TEST_EVALUATION_TIME")
	if value == "" {
		return
	}
	at, err := parseTestEvaluationTime(value, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "helm_testclock: %v\n", err)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "helm_testclock: evaluation clock fixed at %s (test build only)\n", at.Format(time.RFC3339Nano))
	taskActivityTestClock = func() time.Time { return at }
}

func parseTestEvaluationTime(value string, now time.Time) (time.Time, error) {
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("HELM_TEST_EVALUATION_TIME must be RFC 3339: %w", err)
	}
	if at.After(now) {
		return time.Time{}, errors.New("HELM_TEST_EVALUATION_TIME must not be in the future")
	}
	return at.UTC(), nil
}
