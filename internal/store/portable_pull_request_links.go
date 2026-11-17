package store

import (
	"context"

	"github.com/KanterLabs/helm/internal/prlinks"
)

// Legacy versions cannot describe namespaced forge identities. Refuse the
// export, including v2's omitted section, instead of silently losing links.
func rejectLegacyForgeLinks(ctx context.Context, q portableSQL, projectIDs []string) error {
	where, args := portableIDsClause("a.project_id", projectIDs)
	rows, err := q.QueryContext(ctx, `SELECT a.url FROM github_artifacts a WHERE 1=1`+where, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		identity, e := prlinks.Parse(raw)
		if e != nil {
			return invalid("cannot export invalid retained pull request URL", nil)
		}
		if identity.Provider != "github" {
			return invalid("Gitea pull request links require portable export version 5", nil)
		}
	}
	return rows.Err()
}
