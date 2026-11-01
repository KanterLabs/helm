package httpapi

import (
	"net/http"

	"github.com/KanterLabs/helm/internal/auth"
)

func (s *Server) dispatchGithub(w http.ResponseWriter, r *http.Request, identity auth.Identity, parts []string) bool {
	switch {
	case len(parts) == 1 && parts[0] == "artifact-summaries":
		s.artifactSummaries(w, r, identity)
	case len(parts) == 2 && parts[0] == "integrations" && parts[1] == "github":
		s.githubIntegration(w, r, identity, false)
	case len(parts) == 3 && parts[0] == "integrations" && parts[1] == "github" && parts[2] == "check":
		s.githubIntegration(w, r, identity, true)
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "github":
		s.projectGithub(w, r, identity, parts[1])
	case len(parts) == 4 && parts[0] == "projects" && parts[2] == "github" && parts[3] == "readiness":
		s.projectGithubReadiness(w, r, identity, parts[1])
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "github-audits":
		s.githubAuditStart(w, r, identity, parts[1])
	case (len(parts) == 3 || len(parts) == 4) && parts[0] == "projects" && parts[2] == "github-audit-authorizations":
		id := ""
		if len(parts) == 4 {
			id = parts[3]
		}
		s.githubAuditAuthorizations(w, r, identity, parts[1], id)
	case len(parts) == 3 && parts[0] == "tasks" && parts[2] == "artifacts":
		s.taskArtifacts(w, r, identity, parts[1], "", false)
	case len(parts) == 4 && parts[0] == "tasks" && parts[2] == "artifacts":
		s.taskArtifacts(w, r, identity, parts[1], parts[3], false)
	case len(parts) == 5 && parts[0] == "tasks" && parts[2] == "artifacts" && parts[4] == "refresh":
		s.taskArtifacts(w, r, identity, parts[1], parts[3], true)
	case len(parts) == 3 && parts[0] == "audits":
		switch parts[2] {
		case "job", "cancel", "retry", "evidence", "apply":
			s.githubAuditJob(w, r, identity, parts[1], parts[2])
		default:
			return false
		}
	default:
		return false
	}
	return true
}
