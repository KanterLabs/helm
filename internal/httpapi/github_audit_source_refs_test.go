package httpapi

import (
	"strings"
	"testing"
)

// Failure inventory: docs/AUDIT_SOURCE_RETRIEVAL_FAILURES.md. These exact-source
// isolation cases are written before the AST implementation; they supplement
// the provider-HTTP worker scenario without executing or loading any package.
func TestGithubAuditGoReferenceProvenance(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"constructor", `package app
import sq "database/sql"
func check(){ d, _ := sq.Open("sqlite", "copy"); d.SetMaxIdleConns(8) }
`, ""},
		{"parameter", `package app
import sq "database/sql"
func check(d *sq.DB){ d.SetMaxIdleConns(8) }
`, ""},
		{"field", `package app
import sq "database/sql"
type Server struct { database *sq.DB }
func (s *Server) check(){ s.database.SetMaxIdleConns(8) }
`, ""},
		{"owned-identical-name", `package app
import sq "database/sql"
func check(d *sq.DB, p *Pool){d.SetMaxIdleConns(8);p.SetMaxIdleConns(8)}
`, "SetMaxIdleConns"},
		{"shadowed-import", `package app
import sql "database/sql"
func check(sql *Pool){sql.SetMaxIdleConns(8)}
`, "SetMaxIdleConns"},
		{"shadowed-constructor", `package app
import sql "database/sql"
func check(){d,_:=sql.Open("sqlite","copy");{d:=new(Pool);d.SetMaxIdleConns(8)}}
`, "SetMaxIdleConns"},
		{"defined-owned-type", `package app
import sq "database/sql"
type Owned sq.DB
func check(d *Owned){ d.SetMaxIdleConns(8) }
`, "SetMaxIdleConns"},
		{"stdlib-alias", `package app
import sq "database/sql"
type Alias = sq.DB
func check(d *Alias){ d.SetMaxIdleConns(8) }
`, ""},
		{"reassigned-unknown", `package app
import sq "database/sql"
func check(){d,_:=sq.Open("sqlite","copy"); d=unknown(); d.SetMaxIdleConns(8)}
`, "unknown,SetMaxIdleConns"},
		{"unknown", `package app
func check(d Unknown){ d.SetMaxIdleConns(8) }
`, "SetMaxIdleConns"},
		{"owned-response-name", `package app
func check(d *Pool){d.WriteHeader(8)}
`, "WriteHeader"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := []int{}
			for i := range strings.Split(tc.source, "\n") {
				lines = append(lines, i)
			}
			refs := fileSourceReferences("internal/owner.go", tc.source, lines, nil)
			if strings.Join(refs, ",") != tc.want {
				t.Fatalf("refs=%v want=%q", refs, tc.want)
			}
		})
	}
}
