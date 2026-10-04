package store

import (
	"context"
	"testing"

	"github.com/KanterLabs/helm/internal/db"
)

func TestTicketQueueReceivesIntakeAndFilingMovesTheTicket(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	data := New(database)
	// A project already using the preferred key pushes the queue to the next.
	if _, err := data.CreateProject(ctx, ProjectInput{Key: stringPtrForTest("TKT"), Name: stringPtrForTest("Taken")}, ""); err != nil {
		t.Fatal(err)
	}
	ops, err := data.CreateProject(ctx, ProjectInput{Key: stringPtrForTest("OPS"), Name: stringPtrForTest("Operations")}, "")
	if err != nil {
		t.Fatal(err)
	}

	hook, _, err := data.CreateTicketWebhook(ctx, TicketWebhookInput{Name: "Grafana"}, "")
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	queue, err := data.TicketQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if queue.SystemKind != SystemKindTickets || queue.Key != "TICKET" || queue.Name != "Tickets" || hook.ProjectID != queue.ID {
		t.Fatalf("queue = %+v, webhook project = %s", queue, hook.ProjectID)
	}
	if again, err := data.TicketQueue(ctx); err != nil || again.ID != queue.ID {
		t.Fatalf("second TicketQueue = %v, %v", again.ID, err)
	}
	if _, err := data.UpdateProject(ctx, queue.ID, ProjectInput{Archived: boolPtrForQueueTest(true)}, ""); err == nil {
		t.Fatal("the ticket queue was archived")
	}
	if projects, err := data.ListSearchProjects(ctx, nil, "ticket"); err != nil || len(projects) != 0 {
		t.Fatalf("command search listed the queue: %+v %v", projects, err)
	}

	alert := IntakeAlert{AlertType: "generic", FamilyKey: "disk", ConditionKey: "disk", ResourceName: "nas", ResourceID: "nas", Title: "Disk full", Description: "95%", Priority: "normal", Evidence: map[string]string{}, ReopenAfterCompletion: true}
	route := AlertIntakeRoute{Integration: "webhook-" + hook.ID, ActorName: "Grafana", WebhookID: hook.ID}
	results, err := data.IngestAlerts(ctx, route, []IntakeAlert{alert})
	if err != nil || len(results) != 1 || results[0].TaskKey != "TICKET-1" {
		t.Fatalf("ingest = %+v, %v", results, err)
	}

	filed, err := data.FileTicket(ctx, results[0].TaskID, "ops", "")
	if err != nil {
		t.Fatalf("file ticket: %v", err)
	}
	if filed.ProjectID != ops.ID || filed.Key != "OPS-1" {
		t.Fatalf("filed = %s %s", filed.ProjectID, filed.Key)
	}
	if old, err := data.ResolveTaskReference(ctx, "ticket-1"); err != nil || old.ID != filed.ID {
		t.Fatalf("old key resolves to %v, %v", old.ID, err)
	}
	if _, err := data.FileTicket(ctx, filed.ID, queue.ID, ""); err == nil {
		t.Fatal("a ticket was filed back into the queue")
	}

	// A repeat of the same alert keeps counting on the filed ticket.
	repeat, err := data.IngestAlerts(ctx, route, []IntakeAlert{alert})
	if err != nil || repeat[0].Disposition != "repeated" || repeat[0].OccurrenceCount != 2 {
		t.Fatalf("repeat = %+v, %v", repeat, err)
	}
	next, err := data.CreateTicket(ctx, queue.ID, TaskInput{Title: stringPtrForTest("Printer jammed")}, "")
	if err != nil || next.Key != "TICKET-2" {
		t.Fatalf("manual ticket = %v, %v", next.Key, err)
	}
}

func boolPtrForQueueTest(value bool) *bool { return &value }
