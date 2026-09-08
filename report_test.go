package main

import "testing"

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
