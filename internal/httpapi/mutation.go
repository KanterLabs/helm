package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"strings"
)

func (s *Server) rejectAuthIdempotencyKey(w http.ResponseWriter, r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		return true
	}
	s.writeError(w, http.StatusBadRequest, "idempotency_not_supported", "Idempotency-Key is not supported on authentication routes", nil)
	return false
}

func bodyBytes(r *http.Request) []byte {
	if body, ok := r.Context().Value(requestBodyKey).([]byte); ok {
		return body
	}
	return nil
}

// idempotencyRequestHash includes import query options in the request
// identity. Import behavior is selected by dry_run, conflict, and
// target_project query values, so hashing only the JSON body could replay a
// report for a different operation under the same Idempotency-Key.
func idempotencyRequestHash(r *http.Request) string {
	hash := sha256.New()
	_, _ = hash.Write(bodyBytes(r))
	if isPortableImportRequestPath(r.URL.Path) {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(r.URL.Query().Encode()))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func isPortableImportRequestPath(path string) bool {
	if path == "/api/v1/import" || path == "/api/v1/import/trello" {
		return true
	}
	parts := splitPath(strings.TrimPrefix(path, "/api/v1"))
	return len(parts) == 3 && parts[0] == "projects" && parts[2] == "import"
}

func isBodyBearingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (s *Server) mutation(w http.ResponseWriter, r *http.Request, identity auth.Identity, fn func() (int, []byte, string, error)) {
	s.mutationWithAdmission(w, r, identity, s.admitMutationRate, fn)
}

// mutationRateOnly is retained as an alias for lifecycle metadata callers;
// all agent mutations now share the same short-lived rate admission.
func (s *Server) mutationRateOnly(w http.ResponseWriter, r *http.Request, identity auth.Identity, fn func() (int, []byte, string, error)) {
	s.mutationWithAdmission(w, r, identity, s.admitMutationRate, fn)
}

func (s *Server) mutationWithAdmission(w http.ResponseWriter, r *http.Request, identity auth.Identity, admit func(http.ResponseWriter, *http.Request, auth.Identity) bool, fn func() (int, []byte, string, error)) {
	if mutationIdentityIsLimited(identity) {
		s.metricsValue().recordAgentMutation("attempted")
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		if !admit(w, r, identity) {
			return
		}
		status, body, etag, err := fn()
		if err != nil {
			if mutationIdentityIsLimited(identity) {
				s.metricsValue().recordAgentMutation("failed")
			}
			s.writeStoreErrorForIdentity(w, identity, err)
			return
		}
		if mutationIdentityIsLimited(identity) {
			s.metricsValue().recordAgentMutation("succeeded")
		}
		s.writeRaw(w, status, body, etag)
		return
	}
	if len(key) > 255 {
		s.writeError(w, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key is too long", nil)
		return
	}
	requestHash := idempotencyRequestHash(r)
	storeKey := idempotencyStoreKey(identity, key)
	s.idemMu.Lock()
	defer s.idemMu.Unlock()
	record, found, err := s.Store.GetIdempotency(r.Context(), identity.Actor.ID, storeKey, r.Method, r.URL.Path, requestHash)
	if err != nil {
		s.writeStoreErrorForIdentity(w, identity, err)
		return
	}
	if found {
		if record.ResponseLocation != "" {
			w.Header().Set("Location", record.ResponseLocation)
		}
		s.writeRaw(w, record.Status, record.ResponseBody, record.ETag)
		return
	}
	if !admit(w, r, identity) {
		return
	}
	status, body, etag, err := fn()
	if err != nil {
		if mutationIdentityIsLimited(identity) {
			s.metricsValue().recordAgentMutation("failed")
		}
		s.writeStoreErrorForIdentity(w, identity, err)
		return
	}
	if err := s.Store.SaveIdempotency(r.Context(), identity.Actor.ID, storeKey, r.Method, r.URL.Path, requestHash, store.IdempotencyRecord{Status: status, ResponseBody: body, ETag: etag, ResponseLocation: w.Header().Get("Location")}); err != nil {
		if mutationIdentityIsLimited(identity) {
			s.metricsValue().recordAgentMutation("failed")
		}
		s.writeStoreErrorForIdentity(w, identity, err)
		return
	}
	if mutationIdentityIsLimited(identity) {
		s.metricsValue().recordAgentMutation("succeeded")
	}
	s.writeRaw(w, status, body, etag)
}

// idempotencyStoreKey keeps retries for one bearer credential isolated from a
// different credential owned by the same agent actor. The database row still
// uses actor_id as its foreign key, while the token ID becomes part of the
// caller-provided key namespace. Human sessions retain actor-wide replay
// semantics because their authenticated principal is the actor itself.
func idempotencyStoreKey(identity auth.Identity, key string) string {
	if identity.IsToken {
		return "token:" + identity.Token.Token.ID + ":" + key
	}
	return key
}

// idempotencyReplay checks an existing record before handlers resolve a
// mutable resource. A missing record is deliberately not an admission: the
// caller continues through normal validation and mutation handling. Scope and
// administrator checks must run before calling this helper.
func (s *Server) idempotencyReplay(w http.ResponseWriter, r *http.Request, identity auth.Identity) bool {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return false
	}
	if len(key) > 255 {
		s.writeError(w, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key is too long", nil)
		return true
	}
	requestHash := idempotencyRequestHash(r)
	storeKey := idempotencyStoreKey(identity, key)
	s.idemMu.Lock()
	record, found, err := s.Store.GetIdempotency(r.Context(), identity.Actor.ID, storeKey, r.Method, r.URL.Path, requestHash)
	s.idemMu.Unlock()
	if err != nil {
		s.writeStoreError(w, err)
		return true
	}
	if !found {
		return false
	}
	if record.ResponseLocation != "" {
		w.Header().Set("Location", record.ResponseLocation)
	}
	s.writeRaw(w, record.Status, record.ResponseBody, record.ETag)
	return true
}
