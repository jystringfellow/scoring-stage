package yamlout

import "testing"

func dump(t *testing.T, value any) string {
	t.Helper()
	text, err := Dump(value)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestWritesNestedMappingsListsAndScalars(t *testing.T) {
	data := Map{
		{"schemaVersion", "1"},
		{"workspace", Map{{"path", "./src"}, {"clone", true}}},
		{"kits", []any{"docker.io/sbx/shell:1.1.0", Map{{"source", "./kits/aws"}, {"args", map[string]any{"profiles": "dev staging"}}}}},
		{"sandboxOptions", Map{{"cpus", 4}}},
		{"empty", map[string]any{}},
		{"none", []any{}},
	}
	want := `schemaVersion: "1"
workspace:
  path: "./src"
  clone: true
kits:
  - "docker.io/sbx/shell:1.1.0"
  - source: "./kits/aws"
    args:
      profiles: "dev staging"
sandboxOptions:
  cpus: 4
empty: {}
none: []
`
	if got := dump(t, data); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestStringsAreAlwaysQuotedSoValuesCannotChangeTypeOrStructure(t *testing.T) {
	got := dump(t, Map{{"a", "true"}, {"b", "1.20"}, {"c", "x: y\n- z"}, {"d", `it's "q"`}})
	want := "a: \"true\"\nb: \"1.20\"\nc: \"x: y\\n- z\"\nd: \"it's \\\"q\\\"\"\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNonASCIIIsEscaped(t *testing.T) {
	if got := Quote("é😀\x01"); got != `"\u00e9\ud83d\ude00\u0001"` {
		t.Fatalf("got %s", got)
	}
}

func TestKeysThatAreNotPlainIdentifiersAreQuoted(t *testing.T) {
	got := dump(t, Map{{"stage-broker", 1}, {"true", 2}, {"a b", 3}, {"/x", 4}})
	if want := "stage-broker: 1\n\"true\": 2\n\"a b\": 3\n\"/x\": 4\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestGoMapsAreWrittenWithSortedKeys(t *testing.T) {
	got := dump(t, map[string]any{"b": int64(2), "a": nil})
	if want := "a: null\nb: 2\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestListsOfLists(t *testing.T) {
	got := dump(t, Map{{"a", []any{[]any{"x"}, []any{}}}})
	if want := "a:\n  -\n    - \"x\"\n  - []\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUnsupportedValuesAreRefused(t *testing.T) {
	if _, err := Dump(Map{{"a", 1.5}}); err == nil {
		t.Fatal("expected an error for a float")
	}
	if _, err := Dump("scalar"); err == nil {
		t.Fatal("expected an error for a top-level scalar")
	}
}
