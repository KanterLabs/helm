package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Failure mode 14 in test/neglect/API.md (plan case 16): the filtered
// listing must not scan tasks outside the project, the events table, or the
// whole signal table. Runs on a small database always, and on a populated
// database when HELM_NEGLECT_PLAN_DB names one (make neglect-bench, case 16);
// HELM_NEGLECT_PLAN_RECEIPT receives the plan as JSON. Written before the
// listing code.

var neglectPlanTables = []string{"tasks", "t", "events", "e", "task_activity_signal", "s"}

func neglectListingPlanForTest(t *testing.T, data *Store, projectID string) ([]string, time.Duration) {
	t.Helper()
	observer := &observedSignalQueries{}
	var main struct {
		query string
		args  []any
	}
	data.observeQuery = func(query string, args []any) {
		observer.observe(query, args)
		if strings.Contains(query, "FROM tasks t JOIN columns c") && main.query == "" {
			main.query, main.args = query, args
		}
	}
	defer func() { data.observeQuery = nil }()
	started := time.Now()
	if _, err := data.ListTaskPage(t.Context(), projectID, TaskFilter{InactiveDays: 14, State: "backlog", ReleaseID: "unassigned", Limit: 200}); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	if main.query == "" {
		t.Fatal("listing query was not observed")
	}
	rows, err := data.DB.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+main.query, main.args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan, elapsed
}

func assertProjectScopedPlan(t *testing.T, plan []string) {
	t.Helper()
	searchedTasks := false
	for _, line := range plan {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "SCAN" {
			for _, table := range neglectPlanTables {
				if fields[1] == table {
					t.Fatalf("plan scans %s: %v", table, plan)
				}
			}
		}
		if strings.HasPrefix(line, "SEARCH t ") && strings.Contains(line, "project_id=?") {
			searchedTasks = true
		}
	}
	if !searchedTasks {
		t.Fatalf("tasks are not searched by project: %v", plan)
	}
}

func TestTaskActivityListingPlanStaysInProject(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, _ := createAgentWorkFixture(t, data, ctx, "PLAN")
	createProjectorTasksForTest(t, data, ctx, project.ID, agent.ID, 5)
	syncProjectorForTest(t, ctx, attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{}))
	plan, _ := neglectListingPlanForTest(t, data, project.ID)
	assertProjectScopedPlan(t, plan)
}

func TestTaskActivityListingPlanOnPopulatedDatabase(t *testing.T) {
	path := os.Getenv("HELM_NEGLECT_PLAN_DB")
	if path == "" {
		t.Skip("HELM_NEGLECT_PLAN_DB names a populated database (make neglect-bench)")
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	data := New(database)
	var projectID string
	var tasks int
	if err := database.QueryRowContext(t.Context(), `SELECT p.id, COUNT(t.id) FROM projects p JOIN tasks t ON t.project_id=p.id
		GROUP BY p.id ORDER BY COUNT(t.id) DESC, p.id LIMIT 1`).Scan(&projectID, &tasks); err != nil {
		t.Fatal(err)
	}
	var total int
	if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM tasks`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	plan, elapsed := neglectListingPlanForTest(t, data, projectID)
	if receipt := os.Getenv("HELM_NEGLECT_PLAN_RECEIPT"); receipt != "" {
		body, _ := json.MarshalIndent(map[string]any{"plan": plan, "project_tasks": tasks, "database_tasks": total,
			"listing_ms": fmt.Sprintf("%.1f", float64(elapsed.Microseconds())/1000)}, "", "  ")
		if err := os.WriteFile(receipt, append(body, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	assertProjectScopedPlan(t, plan)
}
