package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func repoWithTakes(t *testing.T, body string) *RepoConfig {
	t.Helper()
	repoDir := filepath.Join(t.TempDir(), "repo")
	testutil.WriteFile(t, filepath.Join(repoDir, RepoConfigPath), body)
	repo, err := LoadRepoConfig(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestTakesComeFromBooksTheRepositoryAndTheUsersConfigWithoutRepeats(t *testing.T) {
	dir := t.TempDir()
	home := installedBook(t, dir, "records", "takes = [\"~/.tool/runs\", \"/var/log/tool\"]\n")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "books = [\""+filepath.Join(dir, "records")+"\"]\ntakes = [\"~/notes\", \"~/.tool/runs\"]\n")
	repo := repoWithTakes(t, "takes = [\"~/.tool/reports\", \"/var/log/tool\"]\n")

	composition := composeWith(t, load(t, home), Selection{Books: []string{"records"}, Repo: repo})

	if want := []string{"~/.tool/runs", "/var/log/tool", "~/.tool/reports", "~/notes"}; !reflect.DeepEqual(composition.Takes, want) {
		t.Errorf("takes = %v\nwant %v", composition.Takes, want)
	}
	if want := []string{"~/.tool/reports", "/var/log/tool"}; !reflect.DeepEqual(composition.RepoTakes, want) {
		t.Errorf("repository takes = %v\nwant %v", composition.RepoTakes, want)
	}
}

func TestATakeIsACleanAbsoluteOrHomePathInTheSandbox(t *testing.T) {
	for _, take := range []string{"runs", "./runs", "~", "/", "~/", "~/../etc", "/var/../etc", "/var//log", "/var/log/", "~user/runs", "/var/lo\\u0007g", ""} {
		home := filepath.Join(t.TempDir(), "home")
		testutil.WriteFile(t, filepath.Join(home, "config.toml"), "takes = [\""+take+"\"]\n")
		_, err := LoadSettings(home)
		if err == nil || !strings.Contains(err.Error(), "takes:") {
			t.Errorf("take %q: error %v, want a takes error", take, err)
		}
	}
	for file, write := range map[string]func(t *testing.T) error{
		"book": func(t *testing.T) error {
			_, err := LoadBook(book(t, t.TempDir(), "records", "takes = [\"../up\"]\n"))
			return err
		},
		"repository": func(t *testing.T) error {
			repoDir := filepath.Join(t.TempDir(), "repo")
			testutil.WriteFile(t, filepath.Join(repoDir, RepoConfigPath), "takes = [\"../up\"]\n")
			_, err := LoadRepoConfig(repoDir)
			return err
		},
	} {
		if err := write(t); err == nil || !strings.Contains(err.Error(), "takes:") {
			t.Errorf("%s: error %v, want a takes error", file, err)
		}
	}
}

func TestARepositorysTakesNeedApprovalUntilTheListChanges(t *testing.T) {
	home := t.TempDir()
	pending := func(takes ...string) []Approval {
		t.Helper()
		approvals, err := RepoApprovals(&Composition{RepoTakes: takes})
		if err != nil {
			t.Fatal(err)
		}
		missing, err := Unapproved(home, approvals)
		if err != nil {
			t.Fatal(err)
		}
		return missing
	}
	if got := pending(); len(got) != 0 {
		t.Fatalf("no takes still asks for approval: %v", got)
	}
	first := pending("~/.tool/runs")
	if len(first) != 1 || first[0].Label != "takes" || !reflect.DeepEqual(first[0].Details, []string{"copies ~/.tool/runs from the sandbox to your machine before the sandbox is removed"}) {
		t.Fatalf("approval = %+v", first)
	}
	if err := Approve(home, first); err != nil {
		t.Fatal(err)
	}
	if got := pending("~/.tool/runs"); len(got) != 0 {
		t.Errorf("approved takes ask again: %v", got)
	}
	if got := pending("~/.tool/runs", "~/.ssh"); len(got) != 1 {
		t.Errorf("a longer list did not ask again: %v", got)
	}
}

func TestManagedSettingsThatDenyRepositoryKitsAlsoRefuseItsTakes(t *testing.T) {
	managed := filepath.Join(t.TempDir(), "managed.toml")
	testutil.WriteFile(t, managed, "repo_kits = \"deny\"\n")
	original := ManagedPaths
	ManagedPaths = func() []string { return []string{managed} }
	t.Cleanup(func() { ManagedPaths = original })

	_, err := Compose(load(t, filepath.Join(t.TempDir(), "home")), Selection{Repo: repoWithTakes(t, "takes = [\"~/.tool/runs\"]\n")})
	expectError(t, err, "the repository's takes are refused by")
}

func TestABooksApprovalSummaryListsItsTakes(t *testing.T) {
	loaded, err := LoadBook(book(t, t.TempDir(), "records", "takes = [\"~/.tool/runs\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := summarizeBook(*loaded); !reflect.DeepEqual(got, []string{"copies ~/.tool/runs from the sandbox to your machine before the sandbox is removed"}) {
		t.Errorf("summary = %v", got)
	}
}
