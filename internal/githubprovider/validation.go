// Package githubprovider retains offline identity validation solely for
// historical portable archives. It contains no GitHub client or credentials.
package githubprovider

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)

// PullRequestIdentity is the canonical identity stored in legacy archives.
type PullRequestIdentity struct {
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
}

var errInvalidIdentity = errors.New("Invalid GitHub repository, reference, path or pull request.")

func NormalizeRepository(repository string) (string, error) {
	if !repositoryPattern.MatchString(repository) {
		return "", errInvalidIdentity
	}
	for _, s := range strings.Split(repository, "/") {
		if s == "." || s == ".." || strings.Contains(s, "..") {
			return "", errInvalidIdentity
		}
	}
	return strings.ToLower(repository), nil
}

func ParsePullRequestURL(raw string) (PullRequestIdentity, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil || u.Opaque != "" || strings.Contains(u.EscapedPath(), "%") {
		return PullRequestIdentity{}, errInvalidIdentity
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || len(parts) > 5 || parts[2] != "pull" {
		return PullRequestIdentity{}, errInvalidIdentity
	}
	if len(parts) == 5 && parts[4] != "files" && parts[4] != "commits" && parts[4] != "checks" {
		return PullRequestIdentity{}, errInvalidIdentity
	}
	repository, err := NormalizeRepository(parts[0] + "/" + parts[1])
	if err != nil {
		return PullRequestIdentity{}, err
	}
	number, err := strconv.Atoi(parts[3])
	if err != nil || number < 1 || number > 2147483647 {
		return PullRequestIdentity{}, errInvalidIdentity
	}
	return PullRequestIdentity{Repository: repository, Number: number, URL: fmt.Sprintf("https://github.com/%s/pull/%d", repository, number)}, nil
}
