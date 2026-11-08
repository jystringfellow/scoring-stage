package sbx

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	for text, want := range map[string]Version{
		"sbx version v0.51.0 7d55286de0c775148b985ed349f760f1eba06203\n": {0, 51, 0},
		"v1.2.13": {1, 2, 13},
	} {
		got, err := ParseVersion(text)
		if err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v", text, got, err, want)
		}
	}
}

func TestUnreadableVersionIsAnError(t *testing.T) {
	if _, err := ParseVersion("sbx dev build"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestVersionFloor(t *testing.T) {
	for _, c := range []struct {
		found Version
		ok    bool
	}{{Version{0, 50, 9}, false}, {Version{0, 51, 0}, true}, {Version{1, 0, 0}, true}} {
		err := CheckVersion(c.found)
		if c.ok && err != nil {
			t.Errorf("%v: unexpected %v", c.found, err)
		}
		if !c.ok && (err == nil || !strings.Contains(err.Error(), "0.51.0 or later")) {
			t.Errorf("%v: got %v", c.found, err)
		}
	}
}

func rule(overrides map[string]any) map[string]any {
	r := map[string]any{"id": "r", "scope": "global", "decision": "allow", "resource_type": "network", "status": "active"}
	for k, v := range overrides {
		r[k] = v
	}
	return r
}

func TestGlobalAllowRulesIgnoreDeniesSandboxScopesDisabledRulesAndFilesystem(t *testing.T) {
	policy := Policy{Rules: []map[string]any{
		rule(map[string]any{"id": "default-ai-services"}),
		rule(map[string]any{"id": "http", "resource_type": "http"}),
		rule(map[string]any{"id": "deny", "decision": "deny"}),
		rule(map[string]any{"id": "scoped", "scope": "sandbox:stage-x"}),
		rule(map[string]any{"id": "off", "status": "disabled"}),
		rule(map[string]any{"id": "fs", "resource_type": "filesystem"}),
	}}
	if got := GlobalAllowRules(policy); !reflect.DeepEqual(got, []string{"default-ai-services", "http"}) {
		t.Fatalf("got %v", got)
	}
}

func TestRulesWithoutStatusOrIDAreActiveAndNamedQuestionMark(t *testing.T) {
	policy := Policy{Rules: []map[string]any{{"scope": "global", "decision": "allow", "resource_type": "network"}}}
	if got := GlobalAllowRules(policy); !reflect.DeepEqual(got, []string{"?"}) {
		t.Fatalf("got %v", got)
	}
}

func TestEmptyPolicyIsDenyAll(t *testing.T) {
	if got := GlobalAllowRules(Policy{}); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
