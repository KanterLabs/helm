package githubprovider

import (
	"context"
	"encoding/base64"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const maxFileBytes = 256 * 1024
const maxTreeEntries = 5000

func (c *Client) Resolve(ctx context.Context, repository, ref string) (ResolvedRef, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	repository, err := c.scoped(repository)
	if err != nil {
		return ResolvedRef{}, err
	}
	if !validRef(ref) {
		return ResolvedRef{}, providerError("invalid_input")
	}
	generation := c.Generation()
	var commit struct {
		SHA string `json:"sha"`
	}
	if _, err = c.get(ctx, repository, "/repos/"+repository+"/commits/"+url.PathEscape(ref), &commit); err != nil {
		return ResolvedRef{}, err
	}
	if !shaPattern.MatchString(commit.SHA) {
		return ResolvedRef{}, providerError("unavailable")
	}
	var metadata struct {
		ID int64 `json:"id"`
	}
	if _, err = c.get(ctx, repository, "/repos/"+repository, &metadata); err != nil {
		return ResolvedRef{}, err
	}
	if metadata.ID <= 0 || !c.sameGeneration(generation) {
		return ResolvedRef{}, providerError("unavailable")
	}
	return ResolvedRef{Repository: repository, RepositoryID: metadata.ID, Ref: ref, SHA: commit.SHA, URL: "https://github.com/" + repository + "/tree/" + commit.SHA}, nil
}

func (c *Client) ListFiles(ctx context.Context, repository, sha string) ([]TreeEntry, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	repository, err := c.scoped(repository)
	if err != nil {
		return nil, false, err
	}
	if !shaPattern.MatchString(sha) {
		return nil, false, providerError("invalid_input")
	}
	generation := c.Generation()
	var response struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Mode string `json:"mode"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
			Size int64  `json:"size"`
		} `json:"tree"`
	}
	if _, err = c.get(ctx, repository, "/repos/"+repository+"/git/trees/"+sha+"?recursive=1", &response); err != nil {
		return nil, false, err
	}
	entries := []TreeEntry{}
	partial := response.Truncated
	for _, entry := range response.Tree {
		if entry.Type == "tree" {
			continue
		}
		if entry.Type != "blob" || (entry.Mode != "100644" && entry.Mode != "100755") || !validPath(entry.Path) || !shaPattern.MatchString(entry.SHA) || entry.Size < 0 || entry.Size > maxFileBytes {
			partial = true
			continue
		}
		if len(entries) >= maxTreeEntries {
			partial = true
			break
		}
		entries = append(entries, TreeEntry{Path: entry.Path, SHA: entry.SHA, Size: entry.Size})
	}
	if !c.sameGeneration(generation) {
		return nil, false, providerError("unavailable")
	}
	return entries, partial, nil
}

func (c *Client) ReadFile(ctx context.Context, repository, sha, path string) (File, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	repository, err := c.scoped(repository)
	if err != nil {
		return File{}, err
	}
	if !shaPattern.MatchString(sha) || !validPath(path) {
		return File{}, providerError("invalid_input")
	}
	generation := c.Generation()
	entries, _, err := c.ListFiles(ctx, repository, sha)
	if err != nil {
		return File{}, err
	}
	var expectedBlob string
	for _, entry := range entries {
		if entry.Path == path {
			expectedBlob = entry.SHA
			break
		}
	}
	if expectedBlob == "" {
		return File{}, providerError("file_unavailable")
	}
	// A pinned contents request cannot select an arbitrary download URL.
	// Never fetch download_url, git_url, submodule targets or symlink targets.
	var response struct {
		Type         string `json:"type"`
		SHA          string `json:"sha"`
		Size         int64  `json:"size"`
		Encoding     string `json:"encoding"`
		Content      string `json:"content"`
		Target       string `json:"target"`
		SubmoduleURL string `json:"submodule_git_url"`
	}
	if _, err = c.get(ctx, repository, "/repos/"+repository+"/contents/"+escapedPath(path)+"?ref="+sha, &response); err != nil {
		return File{}, err
	}
	if response.Type != "file" || response.Target != "" || response.SubmoduleURL != "" || response.Encoding != "base64" || response.Size < 0 || response.Size > maxFileBytes || response.SHA != expectedBlob {
		return File{}, providerError("file_unavailable")
	}
	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(response.Content, "\n", ""))
	if err != nil || len(content) > maxFileBytes || int64(len(content)) != response.Size || !utf8.Valid(content) || strings.IndexByte(string(content), 0) >= 0 {
		return File{}, providerError("file_unavailable")
	}
	token, err := c.credentials()
	if err != nil {
		return File{}, err
	}
	if strings.Contains(string(content), token) {
		return File{}, providerError("file_unavailable")
	}
	if !c.sameGeneration(generation) {
		return File{}, providerError("unavailable")
	}
	return File{Repository: repository, Path: path, SHA: response.SHA, CommitSHA: sha, URL: "https://github.com/" + repository + "/blob/" + sha + "/" + escapedPath(path), Text: string(content)}, nil
}
