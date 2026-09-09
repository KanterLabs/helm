package httpapi

import (
	"errors"
	"net"
	"net/http"
	"strings"
)

// NewTailnetPrivateHandler wraps the application handler used by the
// secondary private HTTPS listener. The edge gateway's exact source IP is an
// explicit allow-list; requests from every other peer are rejected before
// Helm routes or authenticates the request.
func NewTailnetPrivateHandler(next http.Handler, allowed []string) (http.Handler, error) {
	if next == nil {
		return nil, errors.New("tailnet private handler requires a next handler")
	}
	peers := make(map[string]struct{}, len(allowed))
	for _, raw := range allowed {
		raw = strings.TrimSpace(raw)
		ip := net.ParseIP(raw)
		if ip == nil || ip.IsUnspecified() {
			return nil, errors.New("tailnet private handler allow-list contains an invalid IP")
		}
		peers[ip.String()] = struct{}{}
	}
	if len(peers) == 0 {
		return nil, errors.New("tailnet private handler allow-list is empty")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip, ok := requestRemoteIP(r.RemoteAddr); !ok {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "private route unavailable", http.StatusForbidden)
			return
		} else if _, allowed := peers[ip]; !allowed {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "private route unavailable", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}), nil
}

func requestRemoteIP(remoteAddr string) (string, bool) {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return "", false
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		ip := net.ParseIP(host)
		if ip == nil {
			return "", false
		}
		return ip.String(), true
	}
	ip := net.ParseIP(remoteAddr)
	if ip == nil {
		return "", false
	}
	return ip.String(), true
}
