package httpapi

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"strings"
)

type semanticDeclarationBody struct {
	selector, name string
	start, end     int
}

func semanticDeclarationBodies(name, text string) []semanticDeclarationBody {
	if path.Ext(name) != ".go" {
		return nil
	}
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, name, text, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	out := []semanticDeclarationBody{}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		selector := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) == 1 {
			receiver := fn.Recv.List[0].Type
			if pointer, ok := receiver.(*ast.StarExpr); ok {
				receiver = pointer.X
			}
			if generic, ok := receiver.(*ast.IndexExpr); ok {
				receiver = generic.X
			}
			if generic, ok := receiver.(*ast.IndexListExpr); ok {
				receiver = generic.X
			}
			if ident, ok := receiver.(*ast.Ident); ok {
				selector = ident.Name + "." + selector
			}
		}
		out = append(out, semanticDeclarationBody{selector, fn.Name.Name, positions.Position(fn.Pos()).Line, positions.Position(fn.End()).Line})
	}
	return out
}

func semanticBodyWindows(name string, body semanticDeclarationBody) ([]semanticPath, bool) {
	plans := []semanticPath{}
	for start := body.start; start <= body.end; start += 101 {
		if len(plans) == 3 {
			return plans, true
		}
		end := start + 100
		if end > body.end {
			end = body.end
		}
		plans = append(plans, semanticPath{Path: name, StartLine: start, EndLine: end, Reason: "Pinned function declaration " + body.selector})
	}
	return plans, false
}

func semanticSelectedBody(plan semanticPath, text string) ([]semanticPath, bool, bool) {
	selectors := plan.declarations
	if len(selectors) == 0 {
		selectors = []string{plan.Declaration}
	}
	plans := []semanticPath{}
	truncated := false
	for _, selector := range selectors {
		matches := []semanticDeclarationBody{}
		for _, body := range semanticDeclarationBodies(plan.Path, text) {
			if body.selector == selector || body.name == selector {
				matches = append(matches, body)
			}
		}
		if len(matches) != 1 {
			return nil, false, false
		}
		more, cut := semanticBodyWindows(plan.Path, matches[0])
		truncated = truncated || cut
		if len(plans)+len(more) > 3 {
			return append(plans, more[:3-len(plans)]...), true, true
		}
		plans = append(plans, more...)
	}
	return plans, truncated, true
}

// Reuse the existing compact 40-by-160 symbol envelope. The selector replaces
// a method's parameter list rather than adding another unbounded source index.
func semanticDeclarationLabels(text string) map[int]string {
	labels := map[int]string{}
	for _, body := range semanticDeclarationBodies("source.go", text) {
		labels[body.start] = fmt.Sprintf("func %s [%d:%d]", body.selector, body.start, body.end)
	}
	return labels
}

// A literal concept can name the behavior itself. Prefer its exact declaration
// over earlier field/metric mentions; the model need not guess line numbers.
// Parse only pinned provider text, never packages, imports or local checkout.
func semanticSearchBodies(name, text string, terms []string) ([]semanticPath, bool) {
	plans := []semanticPath{}
	for _, body := range semanticDeclarationBodies(name, text) {
		matched := false
		for _, term := range terms {
			matched = matched || strings.EqualFold(body.name, term) || strings.EqualFold(body.selector, term)
		}
		if !matched {
			continue
		}
		more, truncated := semanticBodyWindows(name, body)
		if len(plans)+len(more) > 3 {
			return append(plans, more[:3-len(plans)]...), true
		}
		plans = append(plans, more...)
		if truncated {
			return plans, true
		}
	}
	return plans, false
}
