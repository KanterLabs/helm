package httpapi

import (
	"context"
	"github.com/KanterLabs/helm/internal/webassets"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

func (s *Server) health(w http.ResponseWriter, r *http.Request, ready bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if ready {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()
		report := s.readiness(ctx)
		status := http.StatusOK
		if !report.Ready {
			status = http.StatusServiceUnavailable
		}
		s.writeJSON(w, status, report)
		return
	}
	response := map[string]any{"status": "ok", "service": "helm"}
	if s.Cfg.ReleaseSHA != "" {
		response["revision"] = s.Cfg.ReleaseSHA
	}
	s.writeJSON(w, http.StatusOK, response)
}

func staticContentType(name string) string {
	// Minimal containers do not necessarily register .webmanifest in the
	// system MIME database. Installability must not depend on host packages.
	if path.Ext(name) == ".webmanifest" {
		return "application/manifest+json"
	}
	return mime.TypeByExtension(path.Ext(name))
}

func (s *Server) staticFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	// The frontend is embedded so a standalone binary does not depend on a
	// source checkout or a runtime web/dist directory. Keep the /web/dist alias
	// for deployments that previously used that path in asset URLs.
	clean := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if strings.HasPrefix(clean, "web/dist/") {
		clean = strings.TrimPrefix(clean, "web/dist/")
	}
	if clean == "." || clean == "" {
		clean = "index.html"
	}
	if data, err := fs.ReadFile(webassets.Dist, clean); err == nil {
		if contentType := staticContentType(clean); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		if strings.HasPrefix(clean, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
		return
	}
	// Client-side routes are extensionless and should resolve to the SPA shell.
	// A missing asset must remain a real 404 rather than returning HTML with a
	// successful status, which otherwise hides broken production bundles.
	if path.Ext(clean) == "" {
		if data, err := fs.ReadFile(webassets.Dist, "index.html"); err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			if r.Method != http.MethodHead {
				_, _ = w.Write(data)
			}
			return
		}
	}
	s.writeError(w, http.StatusNotFound, "not_found", "asset not found", nil)
}

func (s *Server) openAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(openAPIDocument)
}
