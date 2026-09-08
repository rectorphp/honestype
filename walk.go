package main

import (
	"reflect"

	"github.com/rectorphp/php-parser-in-go/pkg/ast"
)

// isNilVertex reports whether an ast.Vertex holds no concrete node.
func isNilVertex(n ast.Vertex) bool {
	v := reflect.ValueOf(n)
	return !v.IsValid() || (v.Kind() == reflect.Ptr && v.IsNil())
}

// walk visits n and every descendant node, calling visit on each.
func walk(n ast.Vertex, visit func(ast.Vertex)) {
	if isNilVertex(n) {
		return
	}
	visit(n)
	eachChild(n, func(c ast.Vertex) { walk(c, visit) })
}

// eachChild calls fn for each direct child Vertex (single or slice fields).
func eachChild(n ast.Vertex, fn func(ast.Vertex)) {
	v := reflect.ValueOf(n).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if !f.CanInterface() {
			continue
		}
		switch child := f.Interface().(type) {
		case ast.Vertex:
			if !isNilVertex(child) {
				fn(child)
			}
		case []ast.Vertex:
			for _, c := range child {
				if !isNilVertex(c) {
					fn(c)
				}
			}
		}
	}
}

// isNestedScope reports whether a node opens a new function scope, so return
// collection must not descend into it.
func isNestedScope(n ast.Vertex) bool {
	switch n.(type) {
	case *ast.StmtFunction, *ast.StmtClassMethod, *ast.ExprClosure, *ast.ExprArrowFunction:
		return true
	}
	return false
}

// collectScopedReturns returns every StmtReturn reachable from stmts without
// crossing into a nested function scope.
func collectScopedReturns(stmts []ast.Vertex) []*ast.StmtReturn {
	var out []*ast.StmtReturn
	var rec func(n ast.Vertex)
	rec = func(n ast.Vertex) {
		if isNilVertex(n) {
			return
		}
		if r, ok := n.(*ast.StmtReturn); ok {
			out = append(out, r)
		}
		eachChild(n, func(c ast.Vertex) {
			if isNestedScope(c) {
				return
			}
			rec(c)
		})
	}
	for _, s := range stmts {
		rec(s)
	}
	return out
}
