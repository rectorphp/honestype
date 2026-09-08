package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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

	fmt.Printf(" [ERROR] Found %d docblock type mismatch(es)\n\n", len(list))
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
