// Package helmdocs embeds the user guides Helm shows as in-app help, so
// "Learn more" links work on private installs without internet access.
// Only finished user guides are embedded; plans stay repository-only.
package helmdocs

import "embed"

//go:embed PUBLIC_ACCESS.md TICKET_WEBHOOKS.md
var files embed.FS

// Pages maps help page names (used in /api/v1/docs/{page}) to guides.
var Pages = map[string]string{
	"public-access":   "PUBLIC_ACCESS.md",
	"ticket-webhooks": "TICKET_WEBHOOKS.md",
}

// Read returns a help page's Markdown.
func Read(page string) ([]byte, bool) {
	name, ok := Pages[page]
	if !ok {
		return nil, false
	}
	data, err := files.ReadFile(name)
	return data, err == nil
}
