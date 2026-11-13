package helmdocs

import (
	"strings"
	"testing"
)

// Every registered help page must be embedded, or its "Learn more" link
// opens an error in the help drawer.
func TestEveryHelpPageIsEmbedded(t *testing.T) {
	for page, name := range Pages {
		data, ok := Read(page)
		if !ok || len(data) == 0 {
			t.Errorf("help page %q (%s) is not embedded", page, name)
		}
	}
	if _, ok := Read("GITEA_OAUTH_PLAN"); ok {
		t.Error("an unregistered name must not resolve")
	}
}

// The Gitea certificate error opens
// openHelp('gitea-oauth', 'private-certificate-authorities'): the page must
// exist and carry the heading whose GitHub-style id is that anchor.
func TestGiteaGuideHasCertificateSection(t *testing.T) {
	data, ok := Read("gitea-oauth")
	if !ok {
		t.Fatal("gitea-oauth help page is not registered")
	}
	if !strings.Contains(string(data), "\n## Private certificate authorities\n") {
		t.Error("gitea-oauth lacks the heading for #private-certificate-authorities")
	}
}
