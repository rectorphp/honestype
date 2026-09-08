package main

import "testing"

func TestParseType(t *testing.T) {
	tests := []struct {
		in       string
		expr     string
		iterable bool
		ok       bool
	}{
		{"int[]", "['v' => ['t' => 'int']]", true, true},
		{"string[]", "['v' => ['t' => 'string']]", true, true},
		{"int[][]", "['v' => ['v' => ['t' => 'int']]]", true, true},
		{"Node[]", "['v' => ['t' => Node::class]]", true, true},
		{"array<string, Foo>", "['k' => 'string', 'v' => ['t' => Foo::class]]", true, true},
		{"list<int>", "['v' => ['t' => 'int']]", true, true},
		{"Collection<Foo>", "['c' => Collection::class, 'v' => ['t' => Foo::class]]", true, true},
		{"?Foo[]", "['v' => ['t' => Foo::class]]", true, true},
		// leaves parse ok but are not iterable, so nothing is checked
		{"string", "['t' => 'string']", false, true},
		{"int", "['t' => 'int']", false, true},
		{"Foo|null", "['t' => Foo::class, 'null' => true]", false, true},
		// a nullable array (Foo[]|null) is not supported
		{"Foo[]|null", "", false, false},
		// unions beyond X|null are skipped
		{"Foo|Bar", "", false, false},
		{"", "", false, false},
	}

	for _, tc := range tests {
		expr, iterable, ok := parseType(tc.in)
		if ok != tc.ok || iterable != tc.iterable || expr != tc.expr {
			t.Errorf("parseType(%q) = (%q, %v, %v), want (%q, %v, %v)",
				tc.in, expr, iterable, ok, tc.expr, tc.iterable, tc.ok)
		}
	}
}

func TestTagType(t *testing.T) {
	tests := []struct {
		line string
		tag  string
		typ  string
		ok   bool
	}{
		{" * @param int[] $nodes", "@param", "int[]", true},
		{"     * @return Foo[]", "@return", "Foo[]", true},
		{" * @param array<int, Foo> $map", "@param", "array<int, Foo>", true},
		{" * @paramfoo int[] $x", "@param", "", false},
		{" * @var int[] $x", "@param", "", false},
		{" * @param", "@param", "", false},
	}

	for _, tc := range tests {
		typ, _, ok := tagType(tc.line, tc.tag)
		if ok != tc.ok || typ != tc.typ {
			t.Errorf("tagType(%q, %q) = (%q, %v), want (%q, %v)",
				tc.line, tc.tag, typ, ok, tc.typ, tc.ok)
		}
	}
}

func TestExtractTypeToken(t *testing.T) {
	tests := []struct {
		in   string
		typ  string
		rest string
	}{
		{"int[] $nodes", "int[]", " $nodes"},
		{"array<int, Foo> $map", "array<int, Foo>", " $map"},
		{"Foo", "Foo", ""},
	}

	for _, tc := range tests {
		typ, rest := extractTypeToken(tc.in)
		if typ != tc.typ || rest != tc.rest {
			t.Errorf("extractTypeToken(%q) = (%q, %q), want (%q, %q)",
				tc.in, typ, rest, tc.typ, tc.rest)
		}
	}
}
