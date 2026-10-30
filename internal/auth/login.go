package auth

import (
	"context"
	"errors"
	"github.com/KanterLabs/helm/internal/store"
	"net"
	"net/http"
	"strings"
	"time"
)

func (m *Manager) Login(ctx context.Context, email, password string, w http.ResponseWriter, secureOverride ...bool) (store.Actor, error) {
	return m.login(ctx, email, password, "unknown", "", w, secureOverride...)
}

// LoginRequest binds the attempt limiter to the network source while retaining
// the generic credential response used by Login. Only the immediate peer is
// trusted; proxy forwarding headers are deliberately not accepted here.
func (m *Manager) LoginRequest(ctx context.Context, email, password string, r *http.Request, w http.ResponseWriter, secureOverride ...bool) (store.Actor, error) {
	source := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(source); err == nil {
		source = host
	}
	if source == "" {
		source = "unknown"
	}
	if len(source) > 128 {
		source = source[:128]
	}
	return m.login(ctx, email, password, source, r.UserAgent(), w, secureOverride...)
}

const (
	loginAttemptWindow  = 15 * time.Minute
	loginAttemptLimit   = 5
	loginAttemptMaxKeys = 1024
)

func (m *Manager) login(ctx context.Context, email, password, source, userAgent string, w http.ResponseWriter, secureOverride ...bool) (store.Actor, error) {
	if m.Mode != "local" {
		return store.Actor{}, &store.Error{Kind: store.ErrForbidden, Message: "password login is only available in local authentication mode"}
	}
	email = strings.TrimSpace(email)
	if !validEmail(email) {
		return store.Actor{}, &store.Error{Kind: store.ErrInvalid, Message: "a valid email is required"}
	}
	if password == "" {
		return store.Actor{}, &store.Error{Kind: store.ErrInvalid, Message: "password is required"}
	}
	key := strings.ToLower(email) + "\x00" + source
	if !m.reserveLoginAttempt(key) {
		return store.Actor{}, &store.Error{Kind: store.ErrForbidden, Message: "invalid email or password"}
	}
	actor, hash, err := m.Store.GetPasswordHash(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_ = CheckPassword(password, dummyPasswordHash())
			return store.Actor{}, &store.Error{Kind: store.ErrForbidden, Message: "invalid email or password"}
		}
		return store.Actor{}, err
	}
	if hash == "" || !CheckPassword(password, hash) {
		if hash == "" {
			_ = CheckPassword(password, dummyPasswordHash())
		}
		return store.Actor{}, &store.Error{Kind: store.ErrForbidden, Message: "invalid email or password"}
	}
	// A local credential is usable only after the human has an explicit
	// workspace membership. This keeps a verified-but-pending Cloudflare
	// identity from becoming an admitted local session merely because an
	// administrator assigned a password before reviewing its role.
	if actor.DisabledAt != nil || actor.Kind != "human" || actor.WorkspaceRole == "" {
		return store.Actor{}, &store.Error{Kind: store.ErrForbidden, Message: "invalid email or password"}
	}
	raw, err := randomSession()
	if err != nil {
		return store.Actor{}, err
	}
	expires := time.Now().UTC().Add(30 * 24 * time.Hour)
	if _, err := m.Store.CreateSessionWithMetadata(ctx, actor.ID, raw, expires, source, userAgent, hash); err != nil {
		return store.Actor{}, err
	}
	m.clearLoginFailures(key)
	secure := m.SecureCookie
	if len(secureOverride) > 0 {
		secure = secureOverride[0]
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: raw, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 60 * 60, Expires: time.Now().UTC().Add(30 * 24 * time.Hour)})
	return actor, nil
}

func (m *Manager) reserveLoginAttempt(key string) bool {
	nowValue := time.Now().UTC()
	m.loginMu.Lock()
	defer m.loginMu.Unlock()
	if m.loginAttempts == nil {
		m.loginAttempts = make(map[string][]time.Time)
	}
	if _, exists := m.loginAttempts[key]; !exists {
		m.evictLoginAttemptKeys(nowValue)
		if len(m.loginAttempts) >= loginAttemptMaxKeys {
			return false
		}
	}
	entries := m.loginAttempts[key]
	cutoff := nowValue.Add(-loginAttemptWindow)
	kept := entries[:0]
	for _, entry := range entries {
		if entry.After(cutoff) {
			kept = append(kept, entry)
		}
	}
	if len(kept) >= loginAttemptLimit {
		m.loginAttempts[key] = kept
		return false
	}
	m.loginAttempts[key] = append(kept, nowValue)
	return true
}

func (m *Manager) evictLoginAttemptKeys(nowValue time.Time) {
	if len(m.loginAttempts) < loginAttemptMaxKeys {
		return
	}
	cutoff := nowValue.Add(-loginAttemptWindow)
	for key, entries := range m.loginAttempts {
		last := time.Time{}
		for _, entry := range entries {
			if entry.After(last) {
				last = entry
			}
		}
		if last.IsZero() || !last.After(cutoff) {
			delete(m.loginAttempts, key)
			if len(m.loginAttempts) < loginAttemptMaxKeys {
				return
			}
			continue
		}
	}
	// Never evict an active lockout to admit another account name. Capacity
	// remains bounded and new keys fail closed until expired space is reclaimed.
}

func (m *Manager) clearLoginFailures(key string) {
	m.loginMu.Lock()
	defer m.loginMu.Unlock()
	delete(m.loginAttempts, key)
}
