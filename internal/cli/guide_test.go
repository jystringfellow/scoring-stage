package cli

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
	"gopkg.in/yaml.v3"
)

func guideSource(t *testing.T) string {
	t.Helper()
	return testutil.ReadFile(t, "guide.txt")
}

func TestInitGuidePrintsTheProcedureInOrderAndNothingElse(t *testing.T) {
	h := setup(t)
	h.expect(0, "init", "--guide")
	got := h.stdout.String()
	if got != guideSource(t) || h.stderr.Len() != 0 || !strings.HasSuffix(got, "\n") || strings.HasSuffix(got, "\n\n") || strings.Contains(got, "stage:") || strings.Contains(got, "\x1b") {
		t.Fatalf("stdout = %q; stderr = %q", got, h.stderr.String())
	}
	previous := -1
	for _, phrase := range []string{"1.", "CI workflow files", "2.", "stage init", "3.", "stage exec", "4.", "Never add a credential", "5.", "approves the repository's kits"} {
		at := strings.Index(got, phrase)
		if at <= previous {
			t.Fatalf("missing or out-of-order phrase %q", phrase)
		}
		previous = at
	}
}

func TestInitGuideWritesNothingAndCallsNoSbx(t *testing.T) {
	want := guideSource(t)
	h := setup(t)
	cwd, repo := t.TempDir(), t.TempDir()
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), "not valid toml [")
	testutil.WriteFile(t, h.managed, "not valid toml [")
	testutil.WriteFile(t, filepath.Join(repo, ".scoring-stage", "config.toml"), "not valid toml [")
	t.Chdir(cwd)
	roots := []string{cwd, repo, h.home}
	before := make([]map[string]string, len(roots))
	for i, root := range roots {
		before[i] = initTree(t, root)
	}
	for _, path := range []string{repo, filepath.Join(cwd, "nonexistent")} {
		for _, dryRun := range []bool{false, true} {
			args := []string{"init", path, "--guide", "--agent", "nosuch", "--book", "nosuch", "--kit", "../invalid"}
			if dryRun {
				args = append(args, "--dry-run")
			}
			h.expect(0, args...)
			if h.stdout.String() != want || h.stderr.Len() != 0 {
				t.Fatalf("stdout = %q; stderr = %q", h.stdout.String(), h.stderr.String())
			}
		}
	}
	for i, root := range roots {
		if !reflect.DeepEqual(before[i], initTree(t, root)) {
			t.Errorf("guide changed %s", root)
		}
	}
	if calls := h.sbx.Calls(); len(calls) != 0 {
		t.Fatalf("called sbx: %v", calls)
	}
}

func TestInitGuideStillRefusesFlagsAndArgumentsItCannotParse(t *testing.T) {
	h := setup(t)
	for _, args := range [][]string{{"--guide", "--bogus"}, {"--guide", "a", "b"}, {"--guide", "--agent"}} {
		h.expect(2, append([]string{"init"}, args...)...)
	}
}

func readmeGuideSection(t *testing.T) string {
	t.Helper()
	readme := testutil.ReadFile(t, "../../README.md")
	_, section, found := strings.Cut(readme, "### Working out what a repository needs\n")
	if !found {
		t.Fatal("README lacks guide subsection")
	}
	section, _, _ = strings.Cut(section, "\nA repository is not trusted")
	return section
}

func TestTheReadmeCopyOfTheGuideIsTheGuide(t *testing.T) {
	section := readmeGuideSection(t)
	_, block, found := strings.Cut(section, "<!-- stage init --guide:begin -->\n```text\n")
	if !found {
		t.Fatal("README lacks guide begin marker")
	}
	block, _, found = strings.Cut(block, "```\n<!-- stage init --guide:end -->")
	if !found || block != guideSource(t) {
		t.Fatalf("README guide differs from source: %q", block)
	}
}

func TestTheSkillCarriesNoCopyOfTheGuideAndTellsTheAgentToRunItAndNeverApprove(t *testing.T) {
	skill := testutil.ReadFile(t, "../../examples/skills/stage-repo-needs/SKILL.md")
	for _, phrase := range []string{"stage init --guide", "propose", "never approve", "--approve-repo-config", "--allow-*", "credentials", "bindings", "secrets"} {
		if !strings.Contains(skill, phrase) {
			t.Errorf("skill lacks %q", phrase)
		}
	}
	_, rest, found := strings.Cut(skill, "---\n")
	frontmatter, _, closed := strings.Cut(rest, "\n---\n")
	var metadata struct{ Name, Description string }
	if err := yaml.Unmarshal([]byte(frontmatter), &metadata); err != nil || !found || !closed || metadata.Name != "stage-repo-needs" || metadata.Description == "" {
		t.Fatalf("invalid skill metadata: %q (%v)", frontmatter, err)
	}
	for _, line := range strings.Split(guideSource(t), "\n") {
		if len(line) > 30 && strings.Contains(skill, line) {
			t.Errorf("skill copies guide line %q", line)
		}
	}
}

func TestTheGuideAssumesNoOtherToolsAndNoNamedAgentProduct(t *testing.T) {
	guide := guideSource(t)
	for _, name := range []string{"kapellmeister", "Claude", "Codex", "Copilot", "Antigravity"} {
		if strings.Contains(strings.ToLower(guide), strings.ToLower(name)) {
			t.Errorf("guide assumes %s", name)
		}
	}
	for _, text := range []string{readmeGuideSection(t), testutil.ReadFile(t, "../../examples/skills/stage-repo-needs/SKILL.md")} {
		if strings.Contains(strings.ToLower(text), "kapellmeister") {
			t.Error("wrapper assumes another tool")
		}
	}
}
