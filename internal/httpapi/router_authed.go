package httpapi

import (
	"github.com/KanterLabs/helm/internal/auth"
	"net/http"
)

func (s *Server) dispatchAuthed(w http.ResponseWriter, r *http.Request, identity auth.Identity, parts []string) {
	if s.dispatchGithub(w, r, identity, parts) {
		return
	}
	if len(parts) == 1 {
		switch parts[0] {
		case "projects":
			s.projects(w, r, identity)
		case "events":
			s.events(w, r, identity)
		case "notifications":
			s.notifications(w, r, identity)
		case "notification-preferences":
			s.notificationPreferences(w, r, identity)
		case "watches":
			s.watches(w, r, identity)
		case "roadmap":
			s.roadmap(w, r, identity, "", false)
		case "my-work":
			s.myWork(w, r, identity)
		case "tickets":
			s.tickets(w, r, identity)
		case "intake":
			s.intake(w, r, identity)
		case "ticket-webhooks":
			s.ticketWebhooks(w, r, identity)
		case "public-endpoints":
			s.publicEndpoints(w, r, identity)
		case "email-intake":
			s.emailIntakes(w, r, identity)
		case "email-inboxes":
			s.emailInboxes(w, r, identity)
		case "cloudflare":
			s.cloudflareConnect(w, r, identity, parts)
		case "sidebar-counts":
			s.sidebarCounts(w, r, identity)
		case "project-intelligence":
			s.projectIntelligence(w, r, identity, "")
		case "search":
			s.search(w, r, identity, "")
		case "views":
			s.savedViews(w, r, identity)
		case "saved-views":
			s.savedViews(w, r, identity)
		case "issues":
			s.issues(w, r, identity)
		case "agents":
			s.agents(w, r, identity)
		case "export":
			s.exportPortable(w, r, identity, "", false)
		case "import":
			s.importPortable(w, r, identity, "", false)
		default:
			s.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
		}
		return
	}
	if len(parts) == 2 && parts[0] == "admin" && parts[1] == "metrics" {
		s.adminMetrics(w, r, identity)
		return
	}
	if len(parts) >= 3 && parts[0] == "admin" && parts[1] == "beta" {
		s.betaSwitchRoute(w, r, identity, parts[2:])
		return
	}
	if parts[0] == "public-endpoints" && len(parts) == 2 {
		s.publicEndpoint(w, r, identity, parts[1])
		return
	}
	if parts[0] == "docs" && len(parts) == 2 {
		s.helpDocument(w, r, parts[1])
		return
	}
	if parts[0] == "cloudflare" {
		s.cloudflareConnect(w, r, identity, parts)
		return
	}
	if parts[0] == "tickets" && len(parts) == 3 && parts[2] == "file" {
		s.fileTicket(w, r, identity, parts[1])
		return
	}
	if parts[0] == "email-inboxes" && (len(parts) == 2 || (len(parts) == 3 && parts[2] == "address")) {
		s.emailInbox(w, r, identity, parts)
		return
	}
	if parts[0] == "email-intake" && len(parts) == 2 {
		s.emailIntake(w, r, identity, parts[1])
		return
	}
	if parts[0] == "public-endpoints" && len(parts) == 3 && parts[2] == "test" {
		s.testPublicEndpoint(w, r, identity, parts[1])
		return
	}
	if parts[0] == "ticket-webhooks" && (len(parts) == 2 || len(parts) == 3) {
		s.ticketWebhook(w, r, identity, parts)
		return
	}
	if parts[0] == "issues" && len(parts) == 2 && parts[1] == "metrics" {
		s.issueMetrics(w, r, identity)
		return
	}
	if parts[0] == "projects" {
		if len(parts) == 2 {
			s.project(w, r, identity, parts[1])
			return
		}
		if len(parts) == 3 {
			switch parts[2] {
			case "tasks":
				s.tasks(w, r, identity, parts[1])
			case "releases":
				s.releases(w, r, identity, parts[1])
			case "task-context":
				s.taskContext(w, r, identity, parts[1])
			case "task-draft":
				s.taskDraft(w, r, identity, parts[1])
			case "tickets":
				s.projectTickets(w, r, identity, parts[1])
			case "timeline":
				s.projectTimeline(w, r, identity, parts[1])
			case "columns":
				s.columns(w, r, identity, parts[1])
			case "labels":
				s.labels(w, r, identity, parts[1])
			case "roadmap":
				s.roadmap(w, r, identity, parts[1], true)
			case "audits":
				s.audits(w, r, identity, parts[1], true)
			case "watch", "watches":
				s.projectWatch(w, r, identity, parts[1])
			case "export":
				s.exportPortable(w, r, identity, parts[1], true)
			case "import":
				s.importPortable(w, r, identity, parts[1], true)
			case "boards":
				s.boards(w, r, identity, parts[1])
			default:
				s.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
			}
			return
		}
		if len(parts) == 4 && parts[2] == "tasks" && (parts[3] == "bulk" || parts[3] == "batch") {
			s.bulkTask(w, r, identity, parts[1])
			return
		}
	}
	if parts[0] == "releases" {
		if len(parts) == 2 {
			s.release(w, r, identity, parts[1], "")
			return
		}
		if len(parts) == 3 && (parts[2] == "complete" || parts[2] == "reopen" || parts[2] == "work-queue") {
			s.release(w, r, identity, parts[1], parts[2])
			return
		}
	}
	if parts[0] == "codex" {
		s.codexAccount(w, r, identity, parts[1:])
		return
	}
	if parts[0] == "project-intelligence" && len(parts) == 2 && parts[1] == "analyze" {
		s.projectIntelligence(w, r, identity, "analyze")
		return
	}
	if parts[0] == "import" && len(parts) == 2 && parts[1] == "trello" {
		s.importTrello(w, r, identity)
		return
	}
	if parts[0] == "columns" && len(parts) == 2 {
		s.column(w, r, identity, parts[1])
		return
	}
	if parts[0] == "tasks" && len(parts) >= 2 {
		if len(parts) == 2 {
			s.task(w, r, identity, parts[1])
			return
		}
		if len(parts) == 3 {
			switch parts[2] {
			case "comments":
				s.comments(w, r, identity, parts[1])
			case "agent-notes":
				s.agentNotes(w, r, identity, parts[1])
			case "timeline":
				s.taskTimeline(w, r, identity, parts[1])
			case "dependencies":
				s.taskDependencies(w, r, identity, parts[1], "")
			case "checklist", "checklists":
				s.taskChecklist(w, r, identity, parts[1], "")
			case "hierarchy", "children", "ancestors", "descendants", "parent":
				s.taskHierarchy(w, r, identity, parts[1], parts[2], "")
			case "progress":
				s.taskProgress(w, r, identity, parts[1])
			case "heartbeat":
				s.taskHeartbeat(w, r, identity, parts[1])
			case "move":
				s.taskMove(w, r, identity, parts[1])
			case "restore":
				s.restoreTask(w, r, identity, parts[1])
			case "reorder":
				s.taskReorder(w, r, identity, parts[1])
			case "claim":
				s.taskAction(w, r, identity, parts[1], "claim")
			case "renew":
				s.taskAction(w, r, identity, parts[1], "renew")
			case "release":
				s.taskAction(w, r, identity, parts[1], "release")
			case "complete":
				s.taskAction(w, r, identity, parts[1], "complete")
			case "block":
				s.taskAction(w, r, identity, parts[1], "block")
			case "triage", "resolve", "reopen":
				s.issueAction(w, r, identity, parts[1], parts[2])
			case "watch", "watches":
				s.taskWatch(w, r, identity, parts[1])
			default:
				s.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
			}
			return
		}
		if len(parts) == 4 && parts[2] == "dependencies" {
			s.taskDependencies(w, r, identity, parts[1], parts[3])
			return
		}
		if len(parts) == 4 && (parts[2] == "checklist" || parts[2] == "checklists") {
			if parts[3] == "reorder" {
				s.taskChecklistReorder(w, r, identity, parts[1])
			} else {
				s.taskChecklist(w, r, identity, parts[1], parts[3])
			}
			return
		}
		if len(parts) == 4 && parts[2] == "comments" {
			s.commentMutation(w, r, identity, parts[1], parts[3])
			return
		}
		if len(parts) == 4 && parts[2] == "agent-notes" {
			s.agentNoteMutation(w, r, identity, parts[1], parts[3])
			return
		}
		if len(parts) == 4 && parts[2] == "children" {
			s.taskHierarchy(w, r, identity, parts[1], parts[2], parts[3])
			return
		}
	}
	if parts[0] == "notifications" && len(parts) == 2 && parts[1] == "read" {
		s.notifications(w, r, identity)
		return
	}
	if parts[0] == "notifications" && len(parts) == 3 && parts[2] == "read" {
		s.notification(w, r, identity, parts[1])
		return
	}
	if parts[0] == "notifications" && len(parts) == 2 {
		s.notification(w, r, identity, parts[1])
		return
	}
	if parts[0] == "watches" && len(parts) == 2 {
		s.watch(w, r, identity, parts[1])
		return
	}
	if parts[0] == "audits" && len(parts) >= 2 {
		if len(parts) == 2 {
			s.audit(w, r, identity, parts[1])
			return
		}
		if len(parts) == 3 {
			switch parts[2] {
			case "findings":
				s.auditFindings(w, r, identity, parts[1])
			case "finalize":
				s.auditFinalize(w, r, identity, parts[1])
			default:
				s.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
			}
			return
		}
	}
	if parts[0] == "views" && len(parts) >= 2 {
		if len(parts) == 2 {
			s.savedView(w, r, identity, parts[1])
			return
		}
		if len(parts) == 3 && (parts[2] == "search" || parts[2] == "tasks") {
			s.search(w, r, identity, parts[1])
			return
		}
	}
	if parts[0] == "saved-views" && len(parts) >= 2 {
		if len(parts) == 2 {
			s.savedView(w, r, identity, parts[1])
			return
		}
		if len(parts) == 3 && (parts[2] == "search" || parts[2] == "tasks") {
			s.search(w, r, identity, parts[1])
			return
		}
	}
	if parts[0] == "search" && len(parts) == 2 && (parts[1] == "tasks" || parts[1] == "projects" || parts[1] == "views") {
		s.search(w, r, identity, "")
		return
	}
	if parts[0] == "audit-findings" && len(parts) == 2 {
		s.auditFinding(w, r, identity, parts[1])
		return
	}
	if parts[0] == "labels" && len(parts) == 2 && r.Method == http.MethodDelete {
		s.deleteLabel(w, r, identity, parts[1])
		return
	}
	if parts[0] == "agents" && len(parts) == 3 && parts[2] == "tokens" && r.Method == http.MethodPost {
		s.createAgentToken(w, r, identity, parts[1])
		return
	}
	if parts[0] == "tokens" && len(parts) == 2 && r.Method == http.MethodDelete {
		s.deleteToken(w, r, identity, parts[1])
		return
	}
	s.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
}
