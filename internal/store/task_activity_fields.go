package store

// taskActivityFields records actual work-scope changes without copying task
// text into events. An explicit empty array distinguishes metadata-only writes
// from legacy events whose payload cannot establish what changed. This is
// additive event evidence, not a schema migration or proof of implementation.
func taskActivityFields(current Task, title, description, columnID string) []string {
	fields := make([]string, 0, 3)
	if title != current.Title {
		fields = append(fields, "title")
	}
	if description != current.Description {
		fields = append(fields, "description")
	}
	if columnID != current.ColumnID {
		fields = append(fields, "column_id")
	}
	return fields
}
