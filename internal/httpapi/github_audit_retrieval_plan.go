package httpapi

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

var retrievalRoute = regexp.MustCompile(`(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+(/[A-Za-z0-9_./{}:-]+)`)
var retrievalQuoted = regexp.MustCompile("`([^`\n]+)`")
var retrievalSymbol = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*(?:[A-Z][A-Za-z0-9_]*)\b`)
var retrievalReference = regexp.MustCompile(`(?:,\s*|\b)([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
var retrievalHandler = regexp.MustCompile(`,\s*(?:[A-Za-z_][A-Za-z0-9_]*\.)?([A-Za-z_][A-Za-z0-9_]*)\s*\)`)
var retrievalPath = regexp.MustCompile(`(?i)\b(?:[A-Za-z0-9_-]+/)*[A-Za-z0-9_.-]+\.(?:go|rs|tsx?|jsx?|py|java|kt|swift|c|cpp|h|cs|rb|php|svelte|vue|sql|sh|yaml|yml|json|toml|md|txt)\b`)
var retrievalRouteOwner = regexp.MustCompile(`(?i)(\b(?:HandleFunc|Handle|MethodFunc|Methods|Route|Get|Post|Put|Patch|Delete|Head|Options|AddRoute|MapGet|MapPost)\s*\(|\b(?:URL\.Path|Pathname|RequestURI)\b|\bpath\s*:)`)
var retrievalRouteRegistration = regexp.MustCompile(`(?i)\b(?:HandleFunc|Handle|MethodFunc|Methods|Route|Get|Post|Put|Patch|Delete|Head|Options|AddRoute|MapGet|MapPost)\s*\(`)
var retrievalDispatchCall = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_][A-Za-z0-9_]*\s*\(`)

type retrievalAnchors struct{ terms, paths []string }

func criterionAnchors(task store.GithubAuditTask, condition store.GithubAuditCondition, tree []githubprovider.TreeEntry) retrievalAnchors {
	text := condition.Text
	anchors := anchorsFromText(text, tree)
	// Behavioral conditions often name no owner. Use the task's quoted goal as
	// ownership context, without turning incidental description prose into terms.
	if len(anchors.terms) == 0 && len(anchors.paths) == 0 {
		for _, goal := range task.Conditions {
			if goal.Origin == "goal" {
				a := anchorsFromText(goal.Text, tree)
				anchors.terms = appendUnique(anchors.terms, a.terms...)
				anchors.paths = appendUnique(anchors.paths, a.paths...)
			}
		}
	}
	return anchors
}

func anchorsFromText(text string, tree []githubprovider.TreeEntry) retrievalAnchors {
	a := retrievalAnchors{}
	for _, name := range retrievalPath.FindAllString(text, -1) {
		a.paths = appendUnique(a.paths, name)
	}
	for _, match := range retrievalRoute.FindAllStringSubmatch(text, -1) {
		a.terms = appendUnique(a.terms, strings.TrimRight(match[1], ".,:;"))
	}
	for _, match := range retrievalQuoted.FindAllStringSubmatch(text, -1) {
		term := strings.TrimSpace(match[1])
		if retrievalPath.MatchString(term) {
			continue
		}
		if literalDiscoveryTerm(term) && (!strings.Contains(term, "/") || strings.HasPrefix(term, "/")) {
			a.terms = appendUnique(a.terms, term)
		}
	}
	for _, term := range retrievalSymbol.FindAllString(text, -1) {
		if strings.ContainsAny(term, "abcdefghijklmnopqrstuvwxyz") && literalDiscoveryTerm(term) {
			a.terms = appendUnique(a.terms, term)
		}
	}
	for _, entry := range tree {
		if strings.Contains(text, entry.Path) {
			a.paths = appendUnique(a.paths, entry.Path)
		}
	}
	if len(a.terms) > 4 {
		a.terms = a.terms[:4]
	}
	return a
}

var literalTerm = regexp.MustCompile(`^[A-Za-z0-9_./{}-]{1,200}$`)

func literalDiscoveryTerm(term string) bool { return literalTerm.MatchString(term) }

func appendUnique(values []string, additions ...string) []string {
	for _, v := range additions {
		if v == "" {
			continue
		}
		found := false
		for _, old := range values {
			if old == v {
				found = true
				break
			}
		}
		if !found {
			values = append(values, v)
		}
	}
	return values
}

func sourceClass(name string) string {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".rst") {
		return "documentation"
	}
	if strings.Contains(lower, "_test.") || strings.Contains(lower, ".test.") || strings.Contains(lower, ".spec.") || strings.HasPrefix(lower, "test/") || strings.Contains(lower, "/tests/") {
		return "test_source"
	}
	switch path.Ext(lower) {
	case ".go", ".rs", ".ts", ".tsx", ".js", ".jsx", ".py", ".java", ".kt", ".swift", ".c", ".cpp", ".h", ".cs", ".rb", ".php", ".svelte", ".vue", ".sql", ".sh", ".yaml", ".yml", ".json", ".toml":
		return "implementation"
	}
	return "other"
}

// Provider search ranking includes prose and operational scripts. Read likely
// application implementation before those mentions, preserving hint order
// within each class. Every read is still revalidated at the selected SHA.
func prioritizeDiscoveryPaths(paths []string) []string {
	paths = append([]string{}, paths...)
	rank := func(name string) int {
		switch sourceClass(name) {
		case "implementation":
			if strings.HasPrefix(name, "internal/") || strings.HasPrefix(name, "src/") {
				return 0
			}
			return 1
		case "test_source":
			return 2
		case "documentation":
			return 3
		}
		return 4
	}
	sort.SliceStable(paths, func(i, j int) bool { return rank(paths[i]) < rank(paths[j]) })
	return paths
}

// This is an ownership fallback, not card-word ranking. Explicit paths and
// content-search hints are scheduled ahead of it. Large prose never enters it.
func likelyOwners(tree []githubprovider.TreeEntry, routes bool) []string {
	entries := append([]githubprovider.TreeEntry{}, tree...)
	rank := func(p string) int {
		lower := strings.ToLower(p)
		base := path.Base(lower)
		if routes {
			for _, owner := range []string{"router", "routes", "server", "http", "handler", "main", "app"} {
				if strings.Contains(base, owner) {
					return 0
				}
			}
		}
		if strings.HasPrefix(lower, "internal/") || strings.HasPrefix(lower, "src/") {
			return 1
		}
		return 2
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := rank(entries[i].Path), rank(entries[j].Path)
		if a != b {
			return a < b
		}
		return entries[i].Path < entries[j].Path
	})
	paths := []string{}
	for _, entry := range entries {
		if sourceClass(entry.Path) == "implementation" && entry.Size <= 64*1024 {
			paths = append(paths, entry.Path)
		}
	}
	return paths
}

func relevantLines(text string, terms []string) []int {
	matches := []int{}
	for i, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "/*") || strings.HasPrefix(trim, "*") || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, "--") {
			continue
		}
		for _, term := range terms {
			start := 0
			for start < len(line) {
				at := strings.Index(line[start:], term)
				if at < 0 {
					break
				}
				at += start
				end := at + len(term)
				boundary := func(b byte) bool {
					return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
				}
				if (at == 0 || !boundary(line[at-1])) && (end == len(line) || !boundary(line[end])) {
					matches = append(matches, i)
					break
				}
				start = end
			}
			if len(matches) > 0 && matches[len(matches)-1] == i {
				break
			}
		}
	}
	return matches
}

// Route mentions inside task fixtures, SQL prose and descriptions are discovery
// distractors. Require a literal route and nearby registration/dispatch syntax
// before treating a route hit as an implementation owner. An explicitly named
// path remains inspectable even when this conservative heuristic cannot locate
// its framework's registration form.
func implementationLines(text string, terms []string) []int {
	lines := strings.Split(text, "\n")
	matches := []int{}
	for _, term := range terms {
		for _, index := range relevantLines(text, []string{term}) {
			if !strings.HasPrefix(term, "/") {
				matches = append(matches, index)
				continue
			}
			literal := false
			for _, quote := range []string{"\"", "'", "`"} {
				for _, verb := range []string{"", "GET ", "POST ", "PUT ", "PATCH ", "DELETE ", "HEAD ", "OPTIONS "} {
					if strings.Contains(lines[index], quote+verb+term+quote) {
						literal = true
					}
				}
			}
			if !literal {
				continue
			}
			start, end := index-2, index+3
			if start < 0 {
				start = 0
			}
			if end > len(lines) {
				end = len(lines)
			}
			context := strings.Join(lines[start:end], "\n")
			// A compatibility/access guard can mention a path and return false
			// without dispatching it. Keep looking for registration or a handler
			// call instead of declaring this criterion's owner resolved.
			if retrievalRouteRegistration.MatchString(context) || retrievalRouteOwner.MatchString(lines[index]) && retrievalDispatchCall.MatchString(strings.Join(lines[index:end], "\n")) {
				matches = append(matches, index)
			}
		}
	}
	sort.Ints(matches)
	return matches
}

func sourceReferences(text string, lines []int, known []string) []string {
	refs := []string{}
	source := strings.Split(text, "\n")
	ignore := map[string]bool{"func": true, "if": true, "for": true, "switch": true, "return": true, "make": true, "len": true, "string": true, "byte": true, "append": true, "Header": true, "Set": true, "WriteHeader": true, "Write": true, "HandleFunc": true, "Handle": true, "GET": true, "POST": true}
	for _, term := range known {
		ignore[term] = true
	}
	for _, index := range lines {
		line := source[index]
		// Direct URL.Path dispatchers commonly call the handler on the next
		// line. Restrict this lookahead to the route branch, ending at return
		// or its closing brace, so an unrelated following route is not fetched.
		if retrievalRouteOwner.MatchString(line) {
			for next := index + 1; next < len(source) && next <= index+3; next++ {
				trim := strings.TrimSpace(source[next])
				if trim == "}" || trim == "return" || strings.HasPrefix(trim, "return ") {
					break
				}
				line += "\n" + source[next]
			}
		}
		for _, pattern := range []*regexp.Regexp{retrievalHandler, retrievalReference} {
			for _, m := range pattern.FindAllStringSubmatch(line, -1) {
				if len(m[1]) >= 3 && !ignore[m[1]] && literalDiscoveryTerm(m[1]) {
					refs = appendUnique(refs, m[1])
				}
			}
		}
	}
	if len(refs) > 3 {
		refs = refs[:3]
	}
	return refs
}
