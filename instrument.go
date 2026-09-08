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
	varNameRe = regexp.MustCompile(`^\$\w+`)
	identRe   = regexp.MustCompile(`^\\?[A-Za-z_][A-Za-z0-9_\\]*$`)
)

var scalarTypes = map[string]bool{
	"string": true, "int": true, "integer": true, "float": true, "double": true,
	"bool": true, "boolean": true, "array": true, "callable": true, "object": true,
	"iterable": true, "scalar": true, "mixed": true, "null": true, "void": true,
}

var builtinCollections = map[string]bool{
	"array": true, "iterable": true, "list": true,
	"non-empty-array": true, "non-empty-list": true,
}

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
	docLines := strings.Split(string(doc.Value), "\n")

	// @param checks, inserted at the top of the body.
	if bodyOpen >= 0 {
		names := paramVarNames(params)
		var calls strings.Builder
		for k, raw := range docLines {
			typ, after, ok := tagType(raw, "@param")
			if !ok {
				continue
			}
			desc, iterable, ok := parseType(typ)
			if !ok || !iterable {
				continue
			}
			varTok := varNameRe.FindString(strings.TrimSpace(after))
			if varTok == "" || !names[strings.TrimPrefix(varTok, "$")] {
				continue // no matching parameter
			}
			line := doc.Position.StartLine + k
			fmt.Fprintf(&calls, "\n    if (\\function_exists('__docblock_check')) \\__docblock_check(%s, %s, __FILE__, %d, 'param %s');",
				varTok, desc, line, varTok)
		}
		if calls.Len() > 0 {
			edits = append(edits, edit{start: bodyOpen, end: bodyOpen, text: calls.String()})
		}
	}

	// @return check, wrapping each scoped return statement.
	for k, raw := range docLines {
		typ, _, ok := tagType(raw, "@return")
		if !ok {
			continue
		}
		desc, iterable, ok := parseType(typ)
		if !ok || !iterable {
			break
		}
		line := doc.Position.StartLine + k
		for _, ret := range collectScopedReturns(stmts) {
			if isNilVertex(ret.Expr) {
				continue // `return;` has nothing to check
			}
			ep := ret.Expr.GetPosition()
			expr := string(src[ep.StartPos:ep.EndPos])
			wrapped := fmt.Sprintf("{ $__dbr = %s; if (\\function_exists('__docblock_check')) \\__docblock_check($__dbr, %s, __FILE__, %d, 'return'); return $__dbr; }",
				expr, desc, line)
			edits = append(edits, edit{start: ret.Position.StartPos, end: ret.Position.EndPos, text: wrapped})
		}
		break // only the first @return
	}

	return edits
}

// tagType extracts the type token following a docblock tag on one line,
// tolerating spaces inside generics/brackets (e.g. `array<int, Foo>`). It
// returns the type, the remainder of the line, and whether the tag matched.
func tagType(rawLine, tag string) (string, string, bool) {
	line := strings.TrimLeft(rawLine, " \t*/") // drop indentation and comment markers (/** * )
	if !strings.HasPrefix(line, tag) {
		return "", "", false
	}
	rest := line[len(tag):]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return "", "", false // e.g. @param vs @paramfoo
	}
	rest = strings.TrimSpace(rest)
	typ, after := extractTypeToken(rest)
	if typ == "" {
		return "", "", false
	}
	return typ, after, true
}

// extractTypeToken reads a type up to the first depth-0 whitespace, so a type
// with internal spaces inside <>, [] or () stays intact.
func extractTypeToken(s string) (string, string) {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '<', '[', '(':
			depth++
		case '>', ']', ')':
			depth--
		case ' ', '\t':
			if depth <= 0 {
				return s[:i], s[i:]
			}
		}
	}
	return s, ""
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

// parseType converts a docblock type into a PHP descriptor literal the runtime
// helper interprets. It reports whether the type is an iterable worth checking
// (an array, list, or typed collection) and whether parsing succeeded.
//
// Descriptor shapes (PHP array literals):
//
//	leaf:      ['t' => 'string']            or ['t' => Foo::class, 'null' => true]
//	container: ['v' => <desc>]              (+ 'k' => 'string', + 'c' => Coll::class)
func parseType(s string) (expr string, iterable bool, ok bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "?")
	if s == "" {
		return "", false, false
	}

	// Trailing [] (top level, since generics use <>): Foo[] , Foo[][].
	if strings.HasSuffix(s, "[]") {
		vexpr, _, vok := parseType(s[:len(s)-2])
		if !vok {
			return "", false, false
		}
		return "['v' => " + vexpr + "]", true, true
	}

	// Generic: Name<...>.
	if i := strings.IndexByte(s, '<'); i >= 0 && strings.HasSuffix(s, ">") {
		name := strings.TrimSpace(s[:i])
		args := splitTopComma(s[i+1 : len(s)-1])
		lname := strings.ToLower(strings.TrimPrefix(name, "\\"))
		builtin := builtinCollections[lname]

		var value string
		var keyPart string
		switch len(args) {
		case 1:
			vexpr, _, vok := parseType(args[0])
			if !vok {
				return "", false, false
			}
			value = vexpr
		case 2:
			if kexpr, kok := leafKeyExpr(args[0]); kok {
				keyPart = "'k' => " + kexpr + ", "
			}
			vexpr, _, vok := parseType(args[1])
			if !vok {
				return "", false, false
			}
			value = vexpr
		default:
			return "", false, false
		}

		if builtin {
			return "[" + keyPart + "'v' => " + value + "]", true, true
		}
		if !identRe.MatchString(name) {
			return "", false, false
		}
		return "['c' => " + name + "::class, " + keyPart + "'v' => " + value + "]", true, true
	}

	// Leaf (scalar keyword or class), optionally `X|null`.
	return leafExpr(s)
}

func leafExpr(s string) (string, bool, bool) {
	nullable := false
	base := ""
	count := 0
	for _, p := range strings.Split(s, "|") {
		p = strings.TrimSpace(p)
		if strings.EqualFold(p, "null") {
			nullable = true
			continue
		}
		base = p
		count++
	}
	if count != 1 {
		return "", false, false // unions beyond `X|null` are not checked
	}
	t, ok := typeExpr(base)
	if !ok {
		return "", false, false
	}
	expr := "['t' => " + t
	if nullable {
		expr += ", 'null' => true"
	}
	return expr + "]", false, true
}

// typeExpr renders a single leaf type: a quoted scalar keyword, or `Name::class`
// so PHP resolves the class against the file's namespace and imports.
func typeExpr(base string) (string, bool) {
	if scalarTypes[strings.ToLower(base)] {
		return "'" + strings.ToLower(base) + "'", true
	}
	if identRe.MatchString(base) {
		return base + "::class", true
	}
	return "", false
}

// leafKeyExpr renders a supported array-key type; ok=false means "do not check
// the key" rather than a hard failure.
func leafKeyExpr(arg string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "string":
		return "'string'", true
	case "int", "integer":
		return "'int'", true
	case "array-key", "int|string", "string|int":
		return "'array-key'", true
	}
	return "", false
}

// splitTopComma splits on commas not nested inside <> or [].
func splitTopComma(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, c := range s {
		switch c {
		case '<', '[':
			depth++
		case '>', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
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
