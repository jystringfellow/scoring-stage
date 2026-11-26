package config

import (
	"path/filepath"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func TestABooksApprovalListsWhatEachRepoRuleGrantsAndAdds(t *testing.T) {
	path := book(t, t.TempDir(), "org", `
		[aws]
		profiles = ["dev"]
		[[repos]]
		match = ["example/api", "Example.Api"]
		kits = ["./kits/api-rules"]
		aws = { profiles = ["api-dev", "api-ci"] }
	`)
	testutil.WriteFile(t, filepath.Join(path, "kits", "api-rules", "kit.yaml"),
		"capabilities:\n  - type: com.docker.sandbox/network-policy@2\n    config:\n      runtime:\n        allow: [api.example.com]\n")
	loaded, err := LoadBook(BuiltinAgents(), path)
	if err != nil {
		t.Fatal(err)
	}
	details := summarizeBook(BuiltinAgents(), *loaded)
	for _, want := range []string{
		"AWS profiles: dev",
		"repos matching example/api, Example.Api: AWS profiles: api-dev api-ci",
		"repos matching example/api, Example.Api: api-rules: network runtime allow: api.example.com",
	} {
		if !contains(details, want) {
			t.Errorf("details %q, want %q", details, want)
		}
	}
}
