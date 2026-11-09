package store

import "time"

// Evaluation instant for neglect listings (test/neglect/API.md "Fixed
// clock"). Production reads the wall clock. taskActivityTestClock is set
// only by task_activity_signal_clock_testclock.go, which exists only in
// binaries built with -tags helm_testclock, so no request, configuration or
// environment can fix the clock in a default build. In-package tests set the
// per-store evaluationClock instead.
var taskActivityTestClock func() time.Time

// evaluationNow is the instant a task collection read evaluates against: the
// agent-work stale cutoff and, for neglect listings, evaluated_at. A keyset
// continuation reuses the instant recorded in its cursor.
func (s *Store) evaluationNow() time.Time {
	if s.evaluationClock != nil {
		return s.evaluationClock().UTC()
	}
	if taskActivityTestClock != nil {
		return taskActivityTestClock().UTC()
	}
	return time.Now().UTC()
}

// observe reports a statement to an in-package test (statement counts and
// EXPLAIN QUERY PLAN); it does nothing in production.
func (s *Store) observe(query string, args []any) {
	if s.observeQuery != nil {
		s.observeQuery(query, args)
	}
}
