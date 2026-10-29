package httpapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path"
	"strconv"
	"strings"
)

// fileSourceReferences parses only the already bounded, pinned provider text.
// It loads no package, dependency, checkout or executable repository content.
// Unknown provenance and non-Go/malformed source retain conservative discovery.
func fileSourceReferences(name, text string, lines []int, known []string) []string {
	if path.Ext(name) != ".go" {
		return sourceReferences(text, lines, known)
	}
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, name, text, 0)
	if err != nil {
		return sourceReferences(text, lines, known)
	}
	p := goReferenceProvenance{imports: map[string]string{}, tainted: map[*ast.Object]bool{}}
	for _, imp := range file.Imports {
		name, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		alias := path.Base(name)
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		// The standard library owns the dotless first import-path namespace.
		// This proves package provenance, never that an unknown result is its type.
		if !strings.Contains(strings.Split(name, "/")[0], ".") && alias != "_" && alias != "." {
			p.imports[alias] = name
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if assign, ok := node.(*ast.AssignStmt); ok {
			for _, lhs := range assign.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Obj != nil && id.Obj.Decl != assign {
					p.tainted[id.Obj] = true
				}
			}
		}
		return true
	})
	selected := map[int]bool{}
	source := strings.Split(text, "\n")
	for _, line := range lines {
		if line < 0 || line >= len(source) {
			continue
		}
		selected[line] = true
		// Preserve bounded next-line route dispatch following for literal anchors.
		if retrievalRouteOwner.MatchString(source[line]) {
			for next := line + 1; next < len(source) && next <= line+3; next++ {
				trim := strings.TrimSpace(source[next])
				if trim == "}" || trim == "return" || strings.HasPrefix(trim, "return ") {
					break
				}
				selected[next] = true
			}
		}
	}
	refs := []string{}
	add := func(name string) {
		if len(name) < 3 || !literalDiscoveryTerm(name) {
			return
		}
		for _, term := range known {
			if name == term {
				return
			}
		}
		refs = appendUnique(refs, name)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !selected[positions.Position(call.Pos()).Line-1] {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if fun.Obj == nil && types.Universe.Lookup(fun.Name) != nil {
				break
			}
			add(fun.Name)
		case *ast.SelectorExpr:
			if p.importPath(fun.X) == "" && p.externalType(fun.X, 0) == "" {
				add(fun.Sel.Name)
			}
		}
		// Registered handlers are references even though passed as values. Ordinary
		// data arguments are not function references merely because they have names.
		line := positions.Position(call.Pos()).Line - 1
		if retrievalRouteRegistration.MatchString(source[line]) && len(call.Args) > 1 {
			switch handler := call.Args[len(call.Args)-1].(type) {
			case *ast.Ident:
				add(handler.Name)
			case *ast.SelectorExpr:
				if p.importPath(handler.X) == "" && p.externalType(handler.X, 0) == "" {
					add(handler.Sel.Name)
				}
			}
		}
		return true
	})
	if len(refs) > 3 {
		refs = refs[:3]
	}
	return refs
}

type goReferenceProvenance struct {
	imports map[string]string
	tainted map[*ast.Object]bool
}

func (p goReferenceProvenance) importPath(expr ast.Expr) string {
	id, ok := expr.(*ast.Ident)
	if !ok || id.Obj != nil {
		return ""
	} // A local binding shadows an import alias.
	return p.imports[id.Name]
}

// declaredType follows lexical declarations, not spelling or a global method
// ignore list. Depth and source size are bounded; uncertain flows stay unknown.
func (p goReferenceProvenance) declaredType(expr ast.Expr, depth int) ast.Expr {
	if depth > 12 {
		return nil
	}
	switch expr := expr.(type) {
	case *ast.ParenExpr:
		return p.declaredType(expr.X, depth+1)
	case *ast.StarExpr:
		return p.declaredType(expr.X, depth+1)
	case *ast.UnaryExpr:
		return p.declaredType(expr.X, depth+1)
	case *ast.CompositeLit:
		return p.declaredType(expr.Type, depth+1)
	case *ast.Ident:
		if expr.Obj == nil || p.tainted[expr.Obj] {
			return nil
		}
		switch decl := expr.Obj.Decl.(type) {
		case *ast.Field:
			return decl.Type
		case *ast.TypeSpec:
			// A newly defined type may have owned methods even when its
			// underlying representation is external. Only an alias inherits it.
			if _, structure := decl.Type.(*ast.StructType); structure || decl.Assign.IsValid() {
				return decl.Type
			}
		case *ast.ValueSpec:
			if decl.Type != nil {
				return decl.Type
			}
			for i, name := range decl.Names {
				if name.Obj == expr.Obj && i < len(decl.Values) {
					return p.declaredType(decl.Values[i], depth+1)
				}
			}
		case *ast.AssignStmt:
			for i, lhs := range decl.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Obj == expr.Obj && i < len(decl.Rhs) {
					return p.declaredType(decl.Rhs[i], depth+1)
				}
			}
		}
	case *ast.SelectorExpr:
		if p.importPath(expr.X) != "" {
			return expr
		}
		owner := p.declaredType(expr.X, depth+1)
		for i := 0; owner != nil && i < 12; i++ {
			if fields, ok := owner.(*ast.StructType); ok {
				for _, field := range fields.Fields.List {
					for _, name := range field.Names {
						if name.Name == expr.Sel.Name {
							return field.Type
						}
					}
				}
				return nil
			}
			next := p.declaredType(owner, depth+1+i)
			if next == owner {
				return nil
			}
			owner = next
		}
	case *ast.CallExpr:
		if id, ok := expr.Fun.(*ast.Ident); ok && id.Obj == nil && id.Name == "new" && len(expr.Args) == 1 {
			return expr.Args[0]
		}
		if fun, ok := expr.Fun.(*ast.SelectorExpr); ok {
			pkg := p.importPath(fun.X)
			// Explicit stdlib factories with known concrete receivers. Do not assume
			// every function's result inherits its callee's package (e.g. context.Value).
			if pkg == "database/sql" && (fun.Sel.Name == "Open" || fun.Sel.Name == "OpenDB") {
				return &ast.SelectorExpr{X: fun.X, Sel: ast.NewIdent("DB")}
			}
		}
	}
	return nil
}

func (p goReferenceProvenance) externalType(expr ast.Expr, depth int) string {
	if depth > 12 {
		return ""
	}
	if expr, ok := expr.(*ast.SelectorExpr); ok {
		if pkg := p.importPath(expr.X); pkg != "" {
			return pkg + "." + expr.Sel.Name
		}
	}
	if call, ok := expr.(*ast.CallExpr); ok {
		if fun, ok := call.Fun.(*ast.SelectorExpr); ok {
			owner := p.externalType(fun.X, depth+1)
			switch {
			case owner == "net/http.ResponseWriter" && fun.Sel.Name == "Header":
				return "net/http.Header"
			case strings.HasPrefix(owner, "database/sql."):
				switch fun.Sel.Name {
				case "QueryRow", "QueryRowContext":
					return "database/sql.Row"
				case "Query", "QueryContext":
					return "database/sql.Rows"
				case "Begin", "BeginTx":
					return "database/sql.Tx"
				}
			}
		}
	}
	if typ := p.declaredType(expr, depth+1); typ != nil && typ != expr {
		return p.externalType(typ, depth+1)
	}
	return ""
}
