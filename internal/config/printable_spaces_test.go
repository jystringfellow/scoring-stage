package config

import (
	"strings"
	"testing"
)

func TestPrintableEscapesInvisibleSeparatorsAndWideSpaces(t *testing.T) {
	for _, r := range []rune{0x2028, 0x2029, 0x061c, 0x2060, 0x2064, 0xe0041, 0xe007f, 0x00a0, 0x2003, 0x205f, 0x3000, 0x00ad, 0x3164} {
		if got := Printable("a" + string(r) + "b"); strings.ContainsRune(got, r) {
			t.Errorf("Printable(%U) = %q, want it escaped", r, got)
		}
	}
	if text := "release/2026-ünïcode a b"; Printable(text) != text {
		t.Errorf("Printable(%q) = %q", text, Printable(text))
	}
}
