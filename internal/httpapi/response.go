package httpapi

import (
	"encoding/json"
	"github.com/KanterLabs/helm/internal/auth"
	"net/http"
)

func (s *Server) writeRaw(w http.ResponseWriter, status int, body []byte, etag string) {
	if etag != "" {
		w.Header().Set("ETag", etag)
	}
	if len(body) > 0 {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	if len(body) > 0 {
		_, _ = w.Write(body)
	}
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		s.writeInternal(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (s *Server) writeCollection(w http.ResponseWriter, data any, next string) {
	s.writeJSON(w, http.StatusOK, map[string]any{"data": data, "next_cursor": next})
}

func (s *Server) writeInternal(w http.ResponseWriter, err error) {
	s.logInternalError(w, err)
	s.writeError(w, http.StatusInternalServerError, "internal_error", "internal server error", nil)
}

func (s *Server) writeError(w http.ResponseWriter, status int, code, message string, details any) {
	if details == nil {
		details = map[string]any{}
	}
	s.writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "details": details}})
}

func requireScope(w http.ResponseWriter, identity auth.Identity, scope string) bool {
	if identity.HasScope(scope) {
		return true
	}
	sDummyWriteError(w, http.StatusForbidden, "insufficient_scope", "token lacks required scope", map[string]any{"required_scope": scope})
	return false
}

func sDummyWriteError(w http.ResponseWriter, status int, code, message string, details any) {
	if details == nil {
		details = map[string]any{}
	}
	body, _ := json.Marshal(map[string]any{"error": map[string]any{"code": code, "message": message, "details": details}})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
