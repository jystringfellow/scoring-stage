package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func installInitBooks(t *testing.T, h *harness) {
	t.Helper()
	var paths []string
	for _, entry := range []struct{ name, description string }{{"backend", "Example services"}, {"frontend", "Example pages"}} {
		dir := filepath.Join(t.TempDir(), entry.name)
		testutil.WriteFile(t, filepath.Join(dir, "book.toml"), fmt.Sprintf("name = %q\ndescription = %q\n", entry.name, entry.description))
		paths = append(paths, fmt.Sprintf("%q", dir))
	}
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), "books = ["+strings.Join(paths, ", ")+"]\n"+fineGrainedMode)
}

func initTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Name() == ".git" && entry.IsDir() {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(root, path)
		if entry.IsDir() {
			tree[rel+"/"] = ""
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			tree[rel] = target
			return err
		}
		data, err := os.ReadFile(path)
		tree[rel] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func expectInitOutput(t *testing.T, h *harness, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(h.stdout.String(), fragment) {
			t.Fatalf("stdout lacks %q:\n%s", fragment, h.stdout.String())
		}
	}
}

func expectNoInitScaffoldGuidance(t *testing.T, h *harness) {
	t.Helper()
	for _, fragment := range []string{"grants the sandbox nothing", "to use it:"} {
		if strings.Contains(h.stdout.String(), fragment) {
			t.Fatalf("described an unscaffolded kit as scaffolded:\n%s", h.stdout.String())
		}
	}
}

func TestInitWritesAConfigNamingOnlyWhatWasAskedFor(t *testing.T) {
	h := setup(t)
	installInitBooks(t, h)
	dir := t.TempDir()
	h.expect(0, "init", dir, "--agent", "claude", "--book", "backend", "--kit", "rules", "--agent", "claude", "--kit", "rules")
	want := "# Written by stage init.\nagents = [\"claude\"]\nbooks = [\"backend\"]\nkits = [\"./.scoring-stage/kits/rules\"]\n"
	if got := testutil.ReadFile(t, filepath.Join(dir, config.RepoConfigPath)); got != want {
		t.Fatalf("config = %q", got)
	}
	expectInitOutput(t, h, "written .scoring-stage/config.toml", "written .scoring-stage/kits/rules/rules.yaml", "frontend - Example pages", "check passed", "review", "commit", "stage up", "--allow-protected-paths")
}

func TestInitWithoutAnAgentFlagWritesNoAgentsKey(t *testing.T) {
	h := setup(t)
	installInitBooks(t, h)
	dir := t.TempDir()
	h.expect(0, "init", dir, "--book", "backend")
	if got := testutil.ReadFile(t, filepath.Join(dir, config.RepoConfigPath)); strings.Contains(got, "agents") {
		t.Fatalf("config = %s", got)
	}
}

func TestInitWithoutFlagsWritesNothingAndSaysWhy(t *testing.T) {
	h := setup(t)
	dir := t.TempDir()
	before := initTree(t, dir)
	t.Chdir(dir)
	h.expect(0, "init")
	if !reflect.DeepEqual(before, initTree(t, dir)) {
		t.Fatal("wrote files")
	}
	expectInitOutput(t, h, "no --agent, --book or --kit given", "known agents:", "no installed books", "check passed")
	h.expect(2, "init", dir, "extra")
}

func TestInitSaysAScaffoldedKitGrantsNothingAndNamesTheNextSteps(t *testing.T) {
	h := setup(t)
	dir := t.TempDir()
	h.expect(0, "init", dir, "--kit", "rules")
	expectInitOutput(t, h,
		"written .scoring-stage/kits/rules/rules.yaml",
		"stage: .scoring-stage/kits/rules/rules.yaml grants the sandbox nothing yet (capabilities: [])\n",
		"stage: to use it: add the capabilities this repository needs to the kit file; then stage up and run the repository's own build and tests inside with stage exec <slug> -- <command> to see what is still missing\n")
}

func TestInitDryRunSaysAScaffoldedKitGrantsNothingAndNamesTheNextSteps(t *testing.T) {
	h := setup(t)
	dir := t.TempDir()
	before := initTree(t, dir)
	h.expect(0, "init", dir, "--kit", "rules", "--dry-run")
	expectInitOutput(t, h,
		"would write .scoring-stage/kits/rules/rules.yaml",
		"stage: .scoring-stage/kits/rules/rules.yaml grants the sandbox nothing yet (capabilities: [])\n",
		"stage: to use it: add the capabilities this repository needs to the kit file; then stage up and run the repository's own build and tests inside with stage exec <slug> -- <command> to see what is still missing\n")
	if !reflect.DeepEqual(before, initTree(t, dir)) {
		t.Fatal("dry run wrote files")
	}
}

func TestInitDoesNotSayAKitGrantsNothingWhenTheKitAlreadyExists(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprint(dryRun), func(t *testing.T) {
			h := setup(t)
			dir := t.TempDir()
			path, content := config.ScaffoldKit("rules")
			testutil.WriteFile(t, filepath.Join(dir, path), content)
			args := []string{"init", dir, "--kit", "rules"}
			if dryRun {
				args = append(args, "--dry-run")
			}
			h.expect(0, args...)
			expectInitOutput(t, h, "kept "+path)
			expectNoInitScaffoldGuidance(t, h)
		})
	}
}

func TestInitSaysWhatASandboxWithNoKitBookOrAgentWouldHave(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprint(dryRun), func(t *testing.T) {
			h := setup(t)
			dir := t.TempDir()
			before := initTree(t, dir)
			args := []string{"init", dir}
			if dryRun {
				args = append(args, "--dry-run")
			}
			h.expect(0, args...)
			expectInitOutput(t, h,
				"stage: a sandbox for this repository would get the base image, stage's own kit and additions from your config\n",
				"stage: this repository adds no agents, books or kits; add --agent, --book or --kit to ask for more\n")
			if !reflect.DeepEqual(before, initTree(t, dir)) {
				t.Fatal("wrote files")
			}
		})
	}
}

func TestInitDoesNotDescribeABareConfigWhenFlagsOrAConfigExist(t *testing.T) {
	for _, selection := range []string{"agent", "book", "kit", "existing"} {
		t.Run(selection, func(t *testing.T) {
			h := setup(t)
			dir := t.TempDir()
			args := []string{"init", dir}
			switch selection {
			case "agent":
				args = append(args, "--agent", "claude")
			case "book":
				installInitBooks(t, h)
				args = append(args, "--book", "backend")
			case "kit":
				args = append(args, "--kit", "rules")
			case "existing":
				testutil.WriteFile(t, filepath.Join(dir, config.RepoConfigPath), "")
			}
			h.expect(0, args...)
			if strings.Contains(h.stdout.String(), "a sandbox for this repository would get") || strings.Contains(h.stdout.String(), "this repository adds no agents") {
				t.Fatalf("described a bare config:\n%s", h.stdout.String())
			}
		})
	}
}

func TestInitDescribesEachScaffoldedKitAndPrintsTheNextStepsOnce(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprint(dryRun), func(t *testing.T) {
			h := setup(t)
			dir := t.TempDir()
			args := []string{"init", dir, "--kit", "rules", "--kit", "other", "--kit", "rules"}
			if dryRun {
				args = append(args, "--dry-run")
			}
			h.expect(0, args...)
			for _, name := range []string{"rules", "other"} {
				line := fmt.Sprintf("stage: .scoring-stage/kits/%s/%s.yaml grants the sandbox nothing yet (capabilities: [])\n", name, name)
				if count := strings.Count(h.stdout.String(), line); count != 1 {
					t.Fatalf("kit message count = %d:\n%s", count, h.stdout.String())
				}
			}
			if count := strings.Count(h.stdout.String(), "stage: to use it:"); count != 1 {
				t.Fatalf("next steps count = %d:\n%s", count, h.stdout.String())
			}
		})
	}
}

func TestInitRefusesABookThatIsNotInstalledAndListsTheInstalledOnesWithTheirDescriptions(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(fmt.Sprint(installed), func(t *testing.T) {
			h := setup(t)
			if installed {
				installInitBooks(t, h)
			}
			dir := t.TempDir()
			h.expect(1, "init", dir, "--book", "missing")
			h.expectStderr("not installed")
			if installed {
				h.expectStderr("backend - Example services")
				h.expectStderr("frontend - Example pages")
			} else {
				h.expectStderr("none")
			}
			if len(initTree(t, dir)) != 1 {
				t.Fatal("wrote files")
			}
		})
	}
}

func TestInitRefusesAnUnknownAgentAndListsTheKnownOnes(t *testing.T) {
	h := setup(t)
	dir := t.TempDir()
	h.expect(1, "init", dir, "--agent", "missing")
	h.expectStderr("known agents:")
	h.expectStderr("claude")
	if len(initTree(t, dir)) != 1 {
		t.Fatal("wrote files")
	}
}

func TestInitRefusesAKitNameThatIsNotOnePathComponent(t *testing.T) {
	h := setup(t)
	dir := t.TempDir()
	for _, name := range []string{"../x", "a/b", ".", "..", "A b"} {
		h.expect(1, "init", dir, "--kit", name)
		h.expectStderr("kit name")
	}
	if len(initTree(t, dir)) != 1 {
		t.Fatal("wrote files")
	}
}

func TestInitLeavesAnExistingConfigAndKitByteIdenticalAndStillChecksThem(t *testing.T) {
	h := setup(t)
	installInitBooks(t, h)
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, config.RepoConfigPath), "# Keep this\nbooks = [\"backend\"]\nkits = [\"./.scoring-stage/kits/rules\"]\n")
	kit := filepath.Join(dir, ".scoring-stage/kits/rules/rules.yaml")
	testutil.WriteFile(t, kit, "schemaVersion: \"3\"\nkind: mixin\ncapabilities: []\n")
	before := initTree(t, dir)
	info, _ := os.Stat(kit)
	confInfo, _ := os.Stat(filepath.Join(dir, config.RepoConfigPath))
	h.expect(0, "init", dir, "--agent", "claude", "--kit", "other")
	if !reflect.DeepEqual(before, initTree(t, dir)) {
		t.Fatal("changed files")
	}
	after, _ := os.Stat(kit)
	confAfter, _ := os.Stat(filepath.Join(dir, config.RepoConfigPath))
	if !info.ModTime().Equal(after.ModTime()) || !confInfo.ModTime().Equal(confAfter.ModTime()) {
		t.Fatal("changed mtimes")
	}
	expectInitOutput(t, h, "kept .scoring-stage/config.toml", "skipped .scoring-stage/kits/other/other.yaml", "--agent claude", "check passed", "nothing changed")
	expectNoInitScaffoldGuidance(t, h)
	testutil.WriteFile(t, filepath.Join(dir, config.RepoConfigPath), "secrets = {}\n")
	before = initTree(t, dir)
	h.expect(1, "init", dir)
	h.expectStderr("a repository cannot set secrets")
	if !reflect.DeepEqual(before, initTree(t, dir)) {
		t.Fatal("changed invalid config")
	}
}

func TestInitKeepsAnExistingKitAndChecksEveryFileInItBeforeWriting(t *testing.T) {
	h := setup(t)
	dir := t.TempDir()
	kit := filepath.Join(dir, ".scoring-stage/kits/rules/rules.yaml")
	content := "# Keep this\ncapabilities: []\n"
	testutil.WriteFile(t, kit, content)
	h.expect(0, "init", dir, "--kit", "rules")
	if testutil.ReadFile(t, kit) != content {
		t.Fatal("changed kit")
	}
	expectInitOutput(t, h, "kept .scoring-stage/kits/rules/rules.yaml")
	other := t.TempDir()
	testutil.WriteFile(t, filepath.Join(other, ".scoring-stage/kits/rules/extra.yaml"), "capabilities:\n  - type: unknown@1\n")
	before := initTree(t, other)
	h.expect(1, "init", other, "--kit", "rules")
	expectNoInitScaffoldGuidance(t, h)
	if !reflect.DeepEqual(before, initTree(t, other)) {
		t.Fatal("wrote before check")
	}
}

func TestInitDoesNotFollowASymbolicLinkOutOfTheRepository(t *testing.T) {
	for _, rel := range []string{".scoring-stage", ".scoring-stage/kits", ".scoring-stage/kits/rules", ".scoring-stage/config.toml", ".scoring-stage/kits/rules/rules.yaml"} {
		t.Run(rel, func(t *testing.T) {
			h := setup(t)
			dir := t.TempDir()
			outside := t.TempDir()
			path := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Fatal(err)
			}
			before := initTree(t, dir)
			outsideBefore := initTree(t, outside)
			h.expect(1, "init", dir, "--kit", "rules")
			expectInitOutput(t, h, "skipped", "symbolic link")
			expectNoInitScaffoldGuidance(t, h)
			if !reflect.DeepEqual(before, initTree(t, dir)) || !reflect.DeepEqual(outsideBefore, initTree(t, outside)) {
				t.Fatal("followed link or changed repository")
			}
		})
	}
}

func TestARepeatedInitChangesNothingAndSaysSo(t *testing.T) {
	h := setup(t)
	dir := t.TempDir()
	args := []string{"init", dir, "--agent", "claude", "--kit", "rules"}
	h.expect(0, args...)
	before := initTree(t, dir)
	h.expect(0, args...)
	if !reflect.DeepEqual(before, initTree(t, dir)) {
		t.Fatal("changed second run")
	}
	expectInitOutput(t, h, "nothing changed", "check passed")
	if strings.Contains(h.stdout.String(), "stage: written") {
		t.Fatal("wrote second run")
	}
}

func TestInitDryRunWritesNothingAndPrintsExactlyWhatItWouldWrite(t *testing.T) {
	h := setup(t)
	dir := t.TempDir()
	before := initTree(t, dir)
	homeBefore := initTree(t, h.home)
	h.expect(0, "init", dir, "--agent", "claude", "--kit", "rules", "--dry-run")
	output := h.stdout.String()
	if !reflect.DeepEqual(before, initTree(t, dir)) || !reflect.DeepEqual(homeBefore, initTree(t, h.home)) {
		t.Fatal("dry run wrote files")
	}
	h.expect(0, "init", dir, "--agent", "claude", "--kit", "rules")
	for _, rel := range []string{config.RepoConfigPath, ".scoring-stage/kits/rules/rules.yaml"} {
		content := testutil.ReadFile(t, filepath.Join(dir, rel))
		if !strings.Contains(output, "would write "+rel) || !strings.Contains(output, "    "+strings.ReplaceAll(strings.TrimSuffix(content, "\n"), "\n", "\n    ")+"\n") {
			t.Fatalf("dry run lacks exact %s content:\n%s", rel, output)
		}
	}
	if !reflect.DeepEqual(homeBefore, initTree(t, h.home)) || len(h.sbx.Calls()) != 0 {
		t.Fatal("init touched sbx or stage home")
	}
}

func TestInitNeverTouchesSbxOrTheStageHome(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, filepath.Join(h.home, "trusted-kits"), "legacy-approval\n")
	before := initTree(t, h.home)
	h.expect(0, "init", t.TempDir(), "--kit", "rules")
	if !reflect.DeepEqual(before, initTree(t, h.home)) || len(h.sbx.Calls()) != 0 {
		t.Fatal("init changed stage home or called sbx")
	}
	if err := os.RemoveAll(h.home); err != nil {
		t.Fatal(err)
	}
	h.expect(0, "init", t.TempDir(), "--agent", "claude")
	if _, err := os.Stat(h.home); !os.IsNotExist(err) {
		t.Fatal("init created stage home")
	}
}

func TestAConfigInitWroteIsAcceptedByTheCodePathUpPlanUses(t *testing.T) {
	for _, kit := range []bool{false, true} {
		t.Run(fmt.Sprint(kit), func(t *testing.T) {
			h := setup(t)
			installInitBooks(t, h)
			args := []string{"init", h.remote.Checkout, "--agent", "claude"}
			if kit {
				args = append(args, "--kit", "rules")
			} else {
				args = append(args, "--book", "backend")
			}
			h.expect(0, args...)
			initOutput := h.stdout.String()
			files := map[string]string{config.RepoConfigPath: testutil.ReadFile(t, filepath.Join(h.remote.Checkout, config.RepoConfigPath))}
			if kit {
				rel := ".scoring-stage/kits/rules/rules.yaml"
				files[rel] = testutil.ReadFile(t, filepath.Join(h.remote.Checkout, rel))
			}
			h.commitToMain(files)
			h.expect(1, "up", h.remote.Checkout, "check", "--plan")
			h.expectStderr("plan stopped before writing")
			for _, detail := range []string{"asks for kits, books or takes you have not approved", "Review them before approving:"} {
				if !strings.Contains(initOutput, detail) {
					t.Fatalf("init lacks %q", detail)
				}
				h.expectStderr(detail)
			}
			if kit {
				if !strings.Contains(initOutput, "kit "+config.Resolve(h.remote.Checkout)+"/.scoring-stage/kits/rules") {
					t.Fatal("init lacks repository kit path")
				}
				h.expectStderr("no capabilities found")
			}
			h.expect(0, "up", h.remote.Checkout, "approved", "--approve-repo-config")
			h.expect(0, "up", h.remote.Checkout, "plan", "--plan")
		})
	}
}

func TestInitRefusesWhenManagedSettingsDenyRepositoryKitsAndWritesNothing(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, h.managed, "repo_kits = \"deny\"\n")
	dir := t.TempDir()
	before := initTree(t, dir)
	h.expect(1, "init", dir, "--kit", "rules")
	h.expectStderr("repo_kits = \"deny\"")
	expectNoInitScaffoldGuidance(t, h)
	if !reflect.DeepEqual(before, initTree(t, dir)) {
		t.Fatal("wrote refused files")
	}
}

func TestInitMessagesNameTheRepositoryNotTheStagingDirectory(t *testing.T) {
	h := setup(t)
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "repo")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	h.expect(0, "init", link, "--kit", "rules", "--dry-run")
	expectInitOutput(t, h, "kit "+config.Resolve(dir)+"/.scoring-stage/kits/rules")
	if strings.Contains(h.stdout.String()+h.stderr.String(), "stage-init-") {
		t.Fatal("leaked staging directory")
	}
}

func TestInitDryRunRecognizesApprovalOfAnExistingKitWithoutChangingItsModes(t *testing.T) {
	h := setup(t)
	dir := t.TempDir()
	rel, content := config.ScaffoldKit("rules")
	testutil.WriteFile(t, filepath.Join(dir, rel), content)
	if err := os.Chmod(filepath.Join(dir, rel), 0666); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(dir, config.RepoConfigPath), config.DraftRepoConfig(nil, nil, []string{"rules"}))
	repo, err := config.LoadRepoConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := config.LoadSettings(h.home)
	if err != nil {
		t.Fatal(err)
	}
	composition, err := config.Compose(settings, config.Selection{Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	approvals, err := config.RepoApprovals(composition, "example")
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Approve(h.home, approvals); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, config.RepoConfigPath)); err != nil {
		t.Fatal(err)
	}
	before := initTree(t, dir)
	homeBefore := initTree(t, h.home)
	h.expect(0, "init", dir, "--kit", "rules", "--dry-run")
	expectInitOutput(t, h, "nothing needs approving")
	if !reflect.DeepEqual(before, initTree(t, dir)) || !reflect.DeepEqual(homeBefore, initTree(t, h.home)) {
		t.Fatal("dry run changed files")
	}
}

func TestInitSkipsTargetsThatAreNotRegularFiles(t *testing.T) {
	for _, rel := range []string{config.RepoConfigPath, ".scoring-stage/kits/rules/rules.yaml"} {
		t.Run(rel, func(t *testing.T) {
			h := setup(t)
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, rel), 0700); err != nil {
				t.Fatal(err)
			}
			before := initTree(t, dir)
			h.expect(1, "init", dir, "--kit", "rules")
			expectInitOutput(t, h, "skipped", "not a regular file")
			expectNoInitScaffoldGuidance(t, h)
			if !reflect.DeepEqual(before, initTree(t, dir)) {
				t.Fatal("changed nonregular target")
			}
		})
	}
}
