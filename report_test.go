package main

import (
	"os"
	"testing"
)

func TestMethodAt(t *testing.T) {
	src := []byte(`<?php
namespace App;

class Foo
{
    /**
     * @param int[] $nodes
     */
    public function bar(
        array $nodes,
        int $x
    ): void {
        foreach ($nodes as $n) {}
    }
}

/**
 * @return string[]
 */
function loose(): array
{
    return [];
}
`)

	tests := []struct {
		line int
		want string
	}{
		{7, "Foo::bar()"}, // docblock line of the method
		{9, "Foo::bar()"}, // signature line
		{19, "loose()"},   // docblock line of the plain function
		{21, "loose()"},   // function keyword line
		{1, ""},           // outside any function
	}

	for _, tc := range tests {
		if got := methodAt(src, tc.line); got != tc.want {
			t.Errorf("methodAt(line=%d) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestFilterSkipped(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/Foo.php"
	src := []byte(`<?php
namespace App;

class Foo
{
    /**
     * @return int[]
     */
    public function bar(): array
    {
        return [];
    }

    /**
     * @return string[]
     */
    public function baz(): array
    {
        return [];
    }
}
`)
	if err := os.WriteFile(file, src, 0o644); err != nil {
		t.Fatal(err)
	}

	list := []mismatch{
		{file: file, line: "7", context: "return[]"},  // Foo::bar()
		{file: file, line: "16", context: "return[]"}, // Foo::baz()
	}

	tests := []struct {
		name  string
		skips skipList
		want  []string // remaining lines
	}{
		{"no skip", nil, []string{"7", "16"}},
		{"skip bar with parens", skipList{"Foo::bar()"}, []string{"16"}},
		{"skip bar without parens", skipList{"Foo::bar"}, []string{"16"}},
		{"skip both", skipList{"Foo::bar()", "Foo::baz()"}, nil},
		{"unknown method keeps all", skipList{"Foo::nope()"}, []string{"7", "16"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterSkipped(list, tc.skips)
			var lines []string
			for _, m := range got {
				lines = append(lines, m.line)
			}
			if len(lines) != len(tc.want) {
				t.Fatalf("filterSkipped() lines = %v, want %v", lines, tc.want)
			}
			for i := range lines {
				if lines[i] != tc.want[i] {
					t.Errorf("filterSkipped() lines = %v, want %v", lines, tc.want)
				}
			}
		})
	}
}

func TestCollapseIndices(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"param $nodes[0]", "param $nodes[]"},
		{"param $matrix[1][2]", "param $matrix[][]"},
		{"return[b]", "return[]"},
		{"param $x", "param $x"},
	}

	for _, tc := range tests {
		if got := collapseIndices(tc.in); got != tc.want {
			t.Errorf("collapseIndices(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
