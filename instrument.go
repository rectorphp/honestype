package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rectorphp/php-parser-in-go/pkg/ast"
	"github.com/rectorphp/php-parser-in-go/pkg/conf"
	"github.com/rectorphp/php-parser-in-go/pkg/parser"
	"github.com/rectorphp/php-parser-in-go/pkg/token"
	"github.com/rectorphp/php-parser-in-go/pkg/version"
)

const marker = "__docblock_check("

var (
	// Single-dimension array types only: string[], Foo[], ?int[], \A\B[].
	arrayTypeRe = regexp.MustCompile(`^\??\\?[A-Za-z_][A-Za-z0-9_\\]*\[\]$`)
	paramTagRe  = regexp.MustCompile(`@param\s+(\S+)\s+(\$\w+)`)
	returnTagRe = regexp.MustCompile(`@return\s+(\S+)`)
)

// edit is a byte-range replacement in the source ([start,end) -> text).
type edit struct {
	start int
	end   int
	text  string
}

// instrumentTree rewrites every eligible .php file under root in place and
// writes the runtime helper next to it. Returns count of changed files.
func instrumentTree(root string) (int, error) {
	info, err := os.Stat(root)
	if err != nil {
		return 0, err
	}

	var files []string
	helperDir := root
	if info.IsDir() {
		err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case "vendor", "node_modules", ".git":
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".php") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	} else {
		files = []string{root}
		helperDir = filepath.Dir(root)
	}

	changed := 0
	for _, f := range files {
		did, err := instrumentFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", f, err)
			continue
		}
		if did {
			changed++
		}
	}

	if err := writeHelper(helperDir); err != nil {
		return changed, err
	}
	return changed, nil
}

// instrumentFile parses one file, injects checks, and rewrites it if changed.
func instrumentFile(path string) (bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if strings.Contains(string(src), marker) {
		return false, nil // already instrumented
	}

	v, err := version.New("8.3")
	if err != nil {
		return false, err
	}
	root, err := parser.Parse(src, conf.Config{Version: v})
	if err != nil {
		return false, err
	}

	var edits []edit
	walk(root, func(n ast.Vertex) {
		switch fn := n.(type) {
		case *ast.StmtFunction:
			doc := docComment(fn.FunctionTkn)
			edits = append(edits, buildEdits(src, doc, fn.Params, bodyOpenEnd(fn.OpenCurlyBracketTkn), fn.Stmts)...)
		case *ast.StmtClassMethod:
			list, _ := fn.Stmt.(*ast.StmtStmtList)
			if list == nil {
				return // abstract / interface method, no body
			}
			doc := docComment(leadingToken(fn.Modifiers), fn.FunctionTkn)
			edits = append(edits, buildEdits(src, doc, fn.Params, bodyOpenEnd(list.OpenCurlyBracketTkn), list.Stmts)...)
		}
	})

	if len(edits) == 0 {
		return false, nil
	}

	out := applyEdits(src, edits)
	return true, os.WriteFile(path, out, 0o644)
}

func bodyOpenEnd(t *token.Token) int {
	if t == nil {
		return -1
	}
	return t.Position.EndPos
}

// buildEdits produces param and return checks for one function-like node.
func buildEdits(src []byte, doc *token.Token, params []ast.Vertex, bodyOpen int, stmts []ast.Vertex) []edit {
	if doc == nil {
		return nil
	}
	var edits []edit

	// @param checks, inserted at the top of the body.
	if bodyOpen >= 0 {
		names := paramVarNames(params)
		var calls strings.Builder
		for _, m := range paramTagRe.FindAllStringSubmatchIndex(string(doc.Value), -1) {
			typ := string(doc.Value[m[2]:m[3]])
			varTok := string(doc.Value[m[4]:m[5]]) // like $names
			base, ok := arrayBase(typ)
			if !ok {
				continue
			}
			name := strings.TrimPrefix(varTok, "$")
			if !names[name] {
				continue // docblock var not an actual parameter
			}
			line := doc.Position.StartLine + newlinesBefore(doc.Value, m[0])
			fmt.Fprintf(&calls, "\n    __docblock_check(%s, %s, __FILE__, %d, 'param %s');",
				varTok, expectedExpr(base), line, varTok)
		}
		if calls.Len() > 0 {
			edits = append(edits, edit{start: bodyOpen, end: bodyOpen, text: calls.String()})
		}
	}

	// @return check, wrapping each scoped return statement.
	rm := returnTagRe.FindSubmatchIndex(doc.Value)
	if rm != nil {
		base, ok := arrayBase(string(doc.Value[rm[2]:rm[3]]))
		if ok {
			line := doc.Position.StartLine + newlinesBefore(doc.Value, rm[0])
			for _, ret := range collectScopedReturns(stmts) {
				if isNilVertex(ret.Expr) {
					continue // `return;` has nothing to check
				}
				ep := ret.Expr.GetPosition()
				expr := string(src[ep.StartPos:ep.EndPos])
				wrapped := fmt.Sprintf("{ $__dbr = %s; __docblock_check($__dbr, %s, __FILE__, %d, 'return'); return $__dbr; }",
					expr, expectedExpr(base), line)
				edits = append(edits, edit{start: ret.Position.StartPos, end: ret.Position.EndPos, text: wrapped})
			}
		}
	}

	return edits
}

// docComment returns the /** */ docblock among the leading tokens of a
// function-like node. The block attaches to whichever token directly follows
// it (a method's `public` modifier, or a plain function's `function` keyword).
func docComment(tokens ...*token.Token) *token.Token {
	for _, tk := range tokens {
		if tk == nil {
			continue
		}
		var last *token.Token
		for _, ff := range tk.FreeFloating {
			if ff.ID == token.T_DOC_COMMENT {
				last = ff
			}
		}
		if last != nil {
			return last
		}
	}
	return nil
}

// leadingToken returns the token of the first modifier (e.g. `public`), used as
// a docblock anchor for methods.
func leadingToken(modifiers []ast.Vertex) *token.Token {
	if len(modifiers) == 0 {
		return nil
	}
	if id, ok := modifiers[0].(*ast.Identifier); ok {
		return id.IdentifierTkn
	}
	return nil
}

// paramVarNames collects the declared parameter variable names (without $).
func paramVarNames(params []ast.Vertex) map[string]bool {
	names := map[string]bool{}
	for _, p := range params {
		param, ok := p.(*ast.Parameter)
		if !ok {
			continue
		}
		v, ok := param.Var.(*ast.ExprVariable)
		if !ok {
			continue
		}
		if id, ok := v.Name.(*ast.Identifier); ok {
			names[strings.TrimPrefix(string(id.Value), "$")] = true
		}
	}
	return names
}

// arrayBase returns the element type of a single-dimension array docblock type.
func arrayBase(typ string) (string, bool) {
	if !arrayTypeRe.MatchString(typ) {
		return "", false
	}
	base := strings.TrimSuffix(typ, "[]")
	base = strings.TrimPrefix(base, "?")
	return base, true
}

func newlinesBefore(b []byte, offset int) int {
	return strings.Count(string(b[:offset]), "\n")
}

var scalarTypes = map[string]bool{
	"string": true, "int": true, "integer": true, "float": true, "double": true,
	"bool": true, "boolean": true, "array": true, "callable": true, "object": true,
	"iterable": true, "scalar": true, "mixed": true, "null": true, "void": true,
}

// expectedExpr renders the PHP expression the check receives as its expected
// type. Scalar keywords become a quoted string; a class type becomes
// `Name::class`, so PHP resolves it against the file's namespace and imports.
func expectedExpr(base string) string {
	if scalarTypes[strings.ToLower(base)] {
		return "'" + strings.ReplaceAll(base, "'", "\\'") + "'"
	}
	return base + "::class"
}

// applyEdits splices edits into src; edits are applied right-to-left so earlier
// offsets stay valid.
func applyEdits(src []byte, edits []edit) []byte {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	out := src
	for _, e := range edits {
		out = append(out[:e.start:e.start], append([]byte(e.text), out[e.end:]...)...)
	}
	return out
}
