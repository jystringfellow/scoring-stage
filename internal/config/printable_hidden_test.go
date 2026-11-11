package config

import "testing"

func TestHiddenCharactersAreFoundWhereControlCharactersAreNot(t *testing.T) {
	for _, text := range []string{"a⁠b", "a؜b", "a­b", "a b", "a\U000e0041b", "aㅤb", "a\xffb"} {
		if !HasHidden(text) {
			t.Errorf("HasHidden(%q) = false", text)
		}
		if HasControl(text) {
			t.Errorf("HasControl(%q) = true, which would refuse it in file names too", text)
		}
	}
	for _, text := range []string{"a\x1bb", "a‮b"} {
		if !HasHidden(text) || !HasControl(text) {
			t.Errorf("%q is not refused by both", text)
		}
	}
	if HasHidden("release/2026-ünïcode") {
		t.Errorf("visible text is refused")
	}
}
