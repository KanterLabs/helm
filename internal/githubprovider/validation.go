package githubprovider

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
var shaPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

func NormalizeRepository(repository string) (string, error) {
	if !repositoryPattern.MatchString(repository) {
		return "", providerError("invalid_input")
	}
	for _, s := range strings.Split(repository, "/") {
		if s == "." || s == ".." || strings.Contains(s, "..") {
			return "", providerError("invalid_input")
		}
	}
	return strings.ToLower(repository), nil
}

func ParsePullRequestURL(raw string) (PullRequestIdentity, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil || u.Opaque != "" || strings.Contains(u.EscapedPath(), "%") {
		return PullRequestIdentity{}, providerError("invalid_input")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || len(parts) > 5 || parts[2] != "pull" {
		return PullRequestIdentity{}, providerError("invalid_input")
	}
	if len(parts) == 5 && parts[4] != "files" && parts[4] != "commits" && parts[4] != "checks" {
		return PullRequestIdentity{}, providerError("invalid_input")
	}
	repository, err := NormalizeRepository(parts[0] + "/" + parts[1])
	if err != nil {
		return PullRequestIdentity{}, err
	}
	number, err := strconv.Atoi(parts[3])
	if err != nil || number < 1 || number > 2147483647 {
		return PullRequestIdentity{}, providerError("invalid_input")
	}
	return PullRequestIdentity{Repository: repository, Number: number, URL: fmt.Sprintf("https://github.com/%s/pull/%d", repository, number)}, nil
}

func validRef(ref string) bool {
	if len(ref) < 1 || len(ref) > 256 || strings.ContainsAny(ref, "\\%?#\x00\r\n") || strings.Contains(ref, "..") || strings.Contains(ref, ":") || strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") {
		return false
	}
	for _, r := range ref {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return path.Clean(ref) == ref
}
func validPath(value string) bool {
	if value == "" || len(value) > 1024 || strings.HasPrefix(value, "/") || path.Clean(value) != value || strings.ContainsAny(value, "\\%?#\x00\r\n") {
		return false
	}
	for _, p := range strings.Split(value, "/") {
		if p == "." || p == ".." || p == "" {
			return false
		}
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func safeGitHubURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || strings.ContainsAny(raw, "\r\n\x00") {
		return ""
	}
	return u.String()
}
func escapedPath(value string) string {
	parts := strings.Split(value, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
