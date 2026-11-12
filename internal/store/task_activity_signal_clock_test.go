//go:build !helm_testclock

package store

import (
	"testing"
	"time"
)

// API.md failure mode 13: a default build ignores HELM_TEST_EVALUATION_TIME;
// only binaries built with -tags helm_testclock contain the code that reads
// it. (Written after task_activity_signal_clock.go; see the TC-380 report.)
func TestTaskActivityEvaluationClockIgnoresEnvironmentInDefaultBuilds(t *testing.T) {
	t.Setenv("HELM_TEST_EVALUATION_TIME", "2001-01-01T00:00:00Z")
	if taskActivityTestClock != nil {
		t.Fatal("a default build installed the test evaluation clock")
	}
	data, _ := newTaskActivitySignalStore(t)
	if got := data.evaluationNow(); time.Since(got).Abs() > time.Minute {
		t.Fatalf("default evaluation clock = %s", got)
	}
}
