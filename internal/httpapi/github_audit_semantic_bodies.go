package httpapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"strings"
)

// A literal concept can name the behavior itself. Prefer its exact declaration
// over earlier field/metric mentions; the model need not guess line numbers.
// Parse only pinned provider text, never packages, imports or local checkout.
func semanticSearchBodies(name, text string, terms []string) ([]semanticPath, bool) {
	if path.Ext(name) != ".go" {
		return nil, false
	}
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, name, text, parser.SkipObjectResolution)
	if err != nil {
		return nil, false
	}
	plans := []semanticPath{}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		matched := false
		for _, term := range terms {
			matched = matched || strings.EqualFold(function.Name.Name, term)
		}
		if !matched {
			continue
		}
		end := positions.Position(function.End()).Line
		for start := positions.Position(function.Pos()).Line; start <= end; {
			if len(plans) == 3 {
				return plans, true
			}
			last := start + 100
			if last > end {
				last = end
			}
			plans = append(plans, semanticPath{Path: name, StartLine: start, EndLine: last, Reason: "Exact concept function declaration"})
			start = last + 1
		}
	}
	return plans, false
}
