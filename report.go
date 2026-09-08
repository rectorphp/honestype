package main

import (
	"bufio"
	"fmt"
	"os"
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
// optionally writes a Checkstyle XML file.
func renderReport(logPath, checkstyleOut string) error {
	f, err := os.Open(logPath)
	if err != nil {
		return err
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
		m := mismatch{parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]}
		key := m.file + "|" + m.line + "|" + m.expected + "|" + m.actual + "|" + m.context
		seen[key] = m
	}
	if err := sc.Err(); err != nil {
		return err
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

	for _, m := range list {
		fmt.Printf("%s:%s  %s: expected %s[] but found '%s' (e.g. %s)\n",
			m.file, m.line, m.context, m.expected, m.actual, m.sample)
	}
	fmt.Printf("\n%d docblock type mismatch(es)\n", len(list))

	if checkstyleOut != "" {
		return writeCheckstyle(checkstyleOut, list)
	}
	return nil
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
			msg := fmt.Sprintf("%s: expected %s[] but found '%s' (e.g. %s)",
				m.context, m.expected, m.actual, m.sample)
			fmt.Fprintf(&b, "    <error line=\"%s\" severity=\"warning\" message=\"%s\" source=\"docblock.type-mismatch\"/>\n",
				xmlEscape(m.line), xmlEscape(msg))
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
