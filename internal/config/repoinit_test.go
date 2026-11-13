package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func TestADraftedRepositoryConfigHoldsOnlyKeysARepositoryMayHaveAndOmitsEmptyOnes(t *testing.T) {
	for _, tc := range []struct {
		agents, books, kits []string
		want                string
	}{
		{want: "# Written by stage init.\n"},
		{agents: []string{"claude"}, books: []string{"backend"}, kits: []string{"rules"}, want: "# Written by stage init.\nagents = [\"claude\"]\nbooks = [\"backend\"]\nkits = [\"./.scoring-stage/kits/rules\"]\n"},
		{books: []string{"backend"}, want: "# Written by stage init.\nbooks = [\"backend\"]\n"},
	} {
		got := DraftRepoConfig(tc.agents, tc.books, tc.kits)
		if got != tc.want {
			t.Fatalf("draft = %q, want %q", got, tc.want)
		}
		dir := t.TempDir()
		testutil.WriteFile(t, filepath.Join(dir, RepoConfigPath), got)
		if _, err := LoadRepoConfig(dir); err != nil {
			t.Fatal(err)
		}
		data, err := readTOML(filepath.Join(dir, RepoConfigPath))
		if err != nil {
			t.Fatal(err)
		}
		for key := range data {
			if !contains(repoKeys, key) {
				t.Fatalf("forbidden key %s", key)
			}
		}
	}
}

func TestADraftIsTheSameForTheSameFlags(t *testing.T) {
	first := DraftRepoConfig([]string{"codex", "claude"}, []string{"backend"}, []string{"rules"})
	if first != DraftRepoConfig([]string{"codex", "claude"}, []string{"backend"}, []string{"rules"}) {
		t.Fatal("draft changed")
	}
}

func TestAScaffoldedKitIsAcceptedByTheRepositoryKitChecksAndGrantsNothing(t *testing.T) {
	dir := t.TempDir()
	path, content := ScaffoldKit("rules")
	if path != ".scoring-stage/kits/rules/rules.yaml" {
		t.Fatalf("path = %q", path)
	}
	for _, forbidden := range []string{"credential", "extends", "hosts"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("scaffold contains %s", forbidden)
		}
	}
	testutil.WriteFile(t, filepath.Join(dir, path), content)
	testutil.WriteFile(t, filepath.Join(dir, RepoConfigPath), DraftRepoConfig(nil, nil, []string{"rules"}))
	repo, err := LoadRepoConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	composition := composeWith(t, load(t, t.TempDir()), Selection{Repo: repo})
	if len(composition.RepoKits) != 1 {
		t.Fatalf("kits = %v", composition.RepoKits)
	}
	summary := SummarizeKit(composition.RepoKits[0].Source)
	if !reflect.DeepEqual(summary, []string{"no capabilities found; read the kit's files before approving"}) {
		t.Fatalf("summary = %v", summary)
	}
	approvals, err := RepoApprovals(composition, "example")
	if err != nil || len(approvals) != 1 {
		t.Fatalf("approvals = %v, %v", approvals, err)
	}
}

func TestABookKeepsItsDescription(t *testing.T) {
	b, err := LoadBook(BuiltinAgents(), book(t, t.TempDir(), "backend", "description = \"Example services\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if b.Description != "Example services" {
		t.Fatalf("description = %q", b.Description)
	}
}
