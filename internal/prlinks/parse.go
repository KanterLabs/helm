// Package prlinks parses durable pull request links entirely offline.
package prlinks

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxURLBytes = 2048
const MaxNumber = 2147483647

var segment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
var label = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var digits = regexp.MustCompile(`^[0-9]+$`)
var ErrInvalid = errors.New("a valid HTTPS GitHub or Gitea pull request URL is required")

type Identity struct {
	Provider   string
	Host       string
	Repository string
	Number     int
	URL        string
}

// StorageRepository preserves the historical GitHub identity and namespaces
// other forges by host so equal owner/repository pairs never collide.
func (i Identity) StorageRepository() string {
	if i.Provider == "github" {
		return i.Repository
	}
	return i.Host + "/" + i.Repository
}

func Parse(raw string) (Identity, error) {
	bad := func() (Identity, error) { return Identity{}, ErrInvalid }
	if raw == "" || !utf8.ValidString(raw) || len(raw) > MaxURLBytes || strings.TrimSpace(raw) != raw {
		return bad()
	}
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == '\\' {
			return bad()
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Host == "" || strings.Contains(u.EscapedPath(), "%") {
		return bad()
	}
	hostname := strings.ToLower(u.Hostname())
	if hostname == "" || len(hostname) > 253 {
		return bad()
	}
	if net.ParseIP(hostname) == nil {
		if strings.ContainsAny(u.Host, "[]") {
			return bad()
		}
		for _, part := range strings.Split(hostname, ".") {
			if !label.MatchString(part) {
				return bad()
			}
		}
	}
	host := hostname
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return bad()
	}
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return bad()
		}
		if n != 443 {
			host = net.JoinHostPort(hostname, strconv.Itoa(n))
		}
	}
	provider, route := "gitea", "pulls"
	if hostname == "github.com" {
		if port != "" && port != "443" {
			return bad()
		}
		provider, route = "github", "pull"
	}
	parts := strings.Split(strings.TrimSuffix(u.Path, "/"), "/")
	if len(parts) < 5 || len(parts) > 6 || parts[0] != "" || parts[3] != route || !digits.MatchString(parts[4]) {
		return bad()
	}
	if len(parts) == 6 && parts[5] != "files" && parts[5] != "commits" && parts[5] != "checks" && !(provider == "gitea" && parts[5] == "diff") {
		return bad()
	}
	for _, part := range parts[1:3] {
		if !segment.MatchString(part) || strings.Contains(part, "..") {
			return bad()
		}
	}
	n, err := strconv.Atoi(parts[4])
	if err != nil || n < 1 || n > MaxNumber {
		return bad()
	}
	repository := strings.ToLower(parts[1] + "/" + parts[2])
	return Identity{provider, host, repository, n, fmt.Sprintf("https://%s/%s/%s/%d", host, repository, route, n)}, nil
}
