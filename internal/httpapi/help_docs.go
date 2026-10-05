package httpapi

import (
	"net/http"

	helmdocs "github.com/KanterLabs/helm/docs"
)

// helpDocument serves GET /api/v1/docs/{page}: an embedded user guide as
// Markdown for the in-app help drawer (any signed-in user).
func (s *Server) helpDocument(w http.ResponseWriter, r *http.Request, page string) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	data, ok := helmdocs.Read(page)
	if !ok {
		s.writeError(w, http.StatusNotFound, "not_found", "help page not found", nil)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}
