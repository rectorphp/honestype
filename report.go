package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/rectorphp/php-parser-in-go/pkg/ast"
	"github.com/rectorphp/php-parser-in-go/pkg/conf"
	"github.com/rectorphp/php-parser-in-go/pkg/parser"
	"github.com/rectorphp/php-parser-in-go/pkg/token"
	"github.com/rectorphp/php-parser-in-go/pkg/version"
)

// mismatch is one deduplicated docblock type violation.
type mismatch struct {
	file     string
	line     string
	context  string
	expected string
	actual   string
	sample   string
}

// renderReport reads the TSV log, deduplicates rows, prints a summary, and
// optionally writes a Checkstyle XML file and/or GitHub Actions annotations.
// renderReport returns the number of distinct mismatches found so the caller
// can exit non-zero when any invalid docblock type is present.
func renderReport(logPath, checkstyleOut string, githubAnnotations bool) (int, error) {
	f, err := os.Open(logPath)
	if os.IsNotExist(err) {
		renderConsole(nil) // no log written means no mismatches
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()

	seen := map[string]mismatch{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 6)
		if len(parts) < 6 {
			continue
		}
		m := mismatch{parts[0], parts[1], collapseIndices(parts[2]), parts[3], parts[4], parts[5]}
		key := m.file + "|" + m.line + "|" + m.expected + "|" + m.actual + "|" + m.context
		seen[key] = m
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}

	list := make([]mismatch, 0, len(seen))
	for _, m := range seen {
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].file != list[j].file {
			return list[i].file < list[j].file
		}
		return list[i].line < list[j].line
	})

	renderConsole(list)

	if githubAnnotations {
		printGitHubAnnotations(list)
	}
	if checkstyleOut != "" {
		if err := writeCheckstyle(checkstyleOut, list); err != nil {
			return len(list), err
		}
	}
	return len(list), nil
}

// collapseIndices turns concrete element paths (`$nodes[0]`, `return[b]`,
// `$matrix[1][2]`) into a single representative (`$nodes[]`, `$matrix[][]`) so
// many bad elements of one param collapse to one finding.
func collapseIndices(context string) string {
	return indexRe.ReplaceAllString(context, "[]")
}

var indexRe = regexp.MustCompile(`\[[^\]]*\]`)

// mismatchMessage is the human-readable finding text shared by all outputs.
func mismatchMessage(m mismatch) string {
	return fmt.Sprintf("%s: expected %s but found '%s' (e.g. %s)", m.context, m.expected, m.actual, m.sample)
}

// renderConsole prints findings PHPStan-style: one boxed table per file with a
// Line column and the finding message, then a summary line.
func renderConsole(list []mismatch) {
	if len(list) == 0 {
		fmt.Print("\n [OK] No docblock type mismatches found\n\n")
		return
	}

	byFile := map[string][]mismatch{}
	var order []string
	for _, m := range list {
		if _, ok := byFile[m.file]; !ok {
			order = append(order, m.file)
		}
		byFile[m.file] = append(byFile[m.file], m)
	}

	// Column widths.
	lineW := len("Line")
	textW := 0
	for _, file := range order {
		if w := len(displayPath(file)); w > textW {
			textW = w
		}
		for _, m := range byFile[file] {
			if w := len(m.line); w > lineW {
				lineW = w
			}
			if w := len(findingText(m)); w > textW {
				textW = w
			}
		}
	}
	if textW > 110 {
		textW = 110
	}

	sep := " " + strings.Repeat("-", lineW+2) + " " + strings.Repeat("-", textW+2)
	fmt.Println()
	for _, file := range order {
		fmt.Println(sep)
		fmt.Printf("  %-*s  %s\n", lineW, "Line", displayPath(file))
		fmt.Println(sep)
		for _, m := range byFile[file] {
			fmt.Printf("  %-*s  %s\n", lineW, m.line, findingText(m))
		}
		fmt.Println(sep)
		fmt.Println()
	}

	renderSnippets(list)

	fmt.Printf(" [ERROR] Found %d docblock type mismatch(es)\n\n", len(list))
}

// renderSnippets prints, per finding, the failing method and a source excerpt of
// five lines above and below the offending line.
func renderSnippets(list []mismatch) {
	for _, m := range list {
		ln, err := strconv.Atoi(m.line)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(m.file)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		if ln < 1 || ln > len(lines) {
			continue
		}

		header := fmt.Sprintf(" %s:%d", displayPath(m.file), ln)
		if method := methodAt(data, ln); method != "" {
			header += "  in " + method
		}
		fmt.Println(header)
		fmt.Printf("   %s\n\n", findingText(m))

		start := ln - 5
		if start < 1 {
			start = 1
		}
		end := ln + 5
		if end > len(lines) {
			end = len(lines)
		}
		width := len(strconv.Itoa(end))
		for i := start; i <= end; i++ {
			marker := "  "
			if i == ln {
				marker = "> "
			}
			fmt.Printf("   %s%*d | %s\n", marker, width, i, lines[i-1])
		}
		fmt.Println()
	}
}

// span is a named source region by line, used to locate the function and class
// enclosing a finding.
type span struct {
	name       string
	start, end int
}

// methodAt parses src and returns the function or method (prefixed with its
// class as `Class::method()`) whose declaration - including its docblock -
// contains line. Empty when the file does not parse or no owner is found.
func methodAt(src []byte, line int) string {
	v, err := version.New("8.3")
	if err != nil {
		return ""
	}
	root, err := parser.Parse(src, conf.Config{Version: v})
	if err != nil {
		return ""
	}

	var funcs, classes []span
	walk(root, func(n ast.Vertex) {
		switch node := n.(type) {
		case *ast.StmtClass:
			if node.Position != nil {
				classes = append(classes, span{identName(node.Name), node.Position.StartLine, node.Position.EndLine})
			}
		case *ast.StmtFunction:
			if node.Position != nil {
				funcs = append(funcs, span{identName(node.Name) + "()", docStart(node.Position.StartLine, docComment(node.FunctionTkn)), node.Position.EndLine})
			}
		case *ast.StmtClassMethod:
			if node.Position != nil {
				doc := docComment(leadingToken(node.Modifiers), node.FunctionTkn)
				funcs = append(funcs, span{identName(node.Name) + "()", docStart(node.Position.StartLine, doc), node.Position.EndLine})
			}
		}
	})

	fn, ok := innermost(funcs, line)
	if !ok {
		return ""
	}
	if cls, ok := innermost(classes, fn.start); ok && cls.name != "" {
		return cls.name + "::" + fn.name
	}
	return fn.name
}

// docStart returns the docblock's start line when present, else the declaration
// line, so a finding on a docblock line still maps to its function.
func docStart(declLine int, doc *token.Token) int {
	if doc != nil {
		return doc.Position.StartLine
	}
	return declLine
}

// innermost returns the tightest span containing line.
func innermost(spans []span, line int) (span, bool) {
	best, found := span{}, false
	for _, s := range spans {
		if line < s.start || line > s.end {
			continue
		}
		if !found || (s.end-s.start) < (best.end-best.start) {
			best, found = s, true
		}
	}
	return best, found
}

// identName reads the name from an Identifier vertex, empty for anything else
// (e.g. an anonymous class).
func identName(n ast.Vertex) string {
	if id, ok := n.(*ast.Identifier); ok {
		return string(id.Value)
	}
	return ""
}

// findingText is the per-line message shown in the console box.
func findingText(m mismatch) string {
	return fmt.Sprintf("%s: expected %s, found '%s' (e.g. %s)", m.context, m.expected, m.actual, m.sample)
}

func displayPath(file string) string {
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, file); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return file
}

// printGitHubAnnotations emits GitHub Actions workflow commands so each finding
// appears as an inline annotation on the changed file and line. Paths are made
// relative to the working directory (the repo root in CI).
func printGitHubAnnotations(list []mismatch) {
	cwd, _ := os.Getwd()
	for _, m := range list {
		file := m.file
		if cwd != "" {
			if rel, err := filepath.Rel(cwd, file); err == nil && !strings.HasPrefix(rel, "..") {
				file = rel
			}
		}
		msg := ghEscapeData(mismatchMessage(m))
		fmt.Printf("::warning file=%s,line=%s::%s\n", ghEscapeProp(file), m.line, msg)
	}
}

// ghEscapeData / ghEscapeProp escape GitHub workflow-command payloads.
func ghEscapeData(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func ghEscapeProp(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ",", "%2C", ":", "%3A").Replace(s)
}

func writeCheckstyle(path string, list []mismatch) error {
	byFile := map[string][]mismatch{}
	var order []string
	for _, m := range list {
		if _, ok := byFile[m.file]; !ok {
			order = append(order, m.file)
		}
		byFile[m.file] = append(byFile[m.file], m)
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<checkstyle version="3.0">` + "\n")
	for _, file := range order {
		fmt.Fprintf(&b, "  <file name=\"%s\">\n", xmlEscape(file))
		for _, m := range byFile[file] {
			fmt.Fprintf(&b, "    <error line=\"%s\" severity=\"warning\" message=\"%s\" source=\"docblock.type-mismatch\"/>\n",
				xmlEscape(m.line), xmlEscape(mismatchMessage(m)))
		}
		b.WriteString("  </file>\n")
	}
	b.WriteString("</checkstyle>\n")

	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}
