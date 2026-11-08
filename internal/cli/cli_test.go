package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/kits"
	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

type harness struct {
	t              *testing.T
	home           string
	sbx            *testutil.FakeSbx
	remote         testutil.Remote
	stdout, stderr bytes.Buffer
	brokerStarts   []string
}

func setup(t *testing.T) *harness {
	t.Helper()
	given := filepath.Join(t.TempDir(), "stage-home")
	h := &harness{t: t, home: config.Resolve(given)}
	t.Setenv("STAGE_HOME", given)
	h.sbx = testutil.NewFakeSbx(t)
	h.remote = testutil.GitHubRemote(t)
	return h
}

func (h *harness) stage(args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	app := &App{
		Stdout:      &h.stdout,
		Stderr:      &h.stderr,
		FindGh:      func() string { return "" },
		BrokerPID:   func(string) int { return 0 },
		BrokerStart: func(home string) error { h.brokerStarts = append(h.brokerStarts, home); return nil },
		IsTerminal:  func() bool { return false },
	}
	return app.Main(args)
}

func (h *harness) expect(code int, args ...string) {
	h.t.Helper()
	if got := h.stage(args...); got != code {
		h.t.Fatalf("stage %v = %d, want %d\nstdout: %s\nstderr: %s", args, got, code, h.stdout.String(), h.stderr.String())
	}
}

func (h *harness) expectStderr(fragment string) {
	h.t.Helper()
	if !strings.Contains(h.stderr.String(), fragment) {
		h.t.Fatalf("stderr %q does not contain %q", h.stderr.String(), fragment)
	}
}

func (h *harness) task(slug string) string { return filepath.Join(h.home, "tasks", slug) }

func (h *harness) anyCall(prefixes ...string) bool {
	for _, call := range h.sbx.Calls() {
		for _, prefix := range prefixes {
			if strings.HasPrefix(call, prefix) {
				return true
			}
		}
	}
	return false
}

func TestUpWritesTheTaskAndEnvFileAndCreatesThroughSbxEnv(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix-login")
	dir := h.task("fix-login")
	var task Task
	if err := json.Unmarshal([]byte(testutil.ReadFile(t, filepath.Join(dir, "task.json"))), &task); err != nil {
		t.Fatal(err)
	}
	if task.Sandbox != "stage-fix-login" || task.GitHubRepo != "acme/api" || task.RepoDir != config.Resolve(filepath.Join(dir, "src")) {
		t.Errorf("task = %+v", task)
	}
	env := testutil.ReadFile(t, filepath.Join(dir, "sbxenv.yaml"))
	for _, fragment := range []string{`name: "stage-fix-login"`, `agent: "shell"`, "clone: true", `"../../kits/` + filepath.Base(kits.Dir(h.home)) + `/stage"`} {
		if !strings.Contains(env, fragment) {
			t.Errorf("env lacks %s:\n%s", fragment, env)
		}
	}
	if strings.Contains(env, "stage-broker") {
		t.Error("env has a broker without aws profiles")
	}
	if _, err := os.Stat(filepath.Join(kits.Dir(h.home), "stage", "stage.yaml")); err != nil {
		t.Error("core kits not extracted")
	}
	if got := h.sbx.LastCall(); got != "env create --force --no-input "+dir {
		t.Errorf("last call %q", got)
	}
	if len(h.brokerStarts) != 0 {
		t.Error("broker started without aws profiles")
	}
}

func TestTaskJSONKeepsItsFieldOrder(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	content := testutil.ReadFile(t, filepath.Join(h.task("fix"), "task.json"))
	var order []string
	for _, key := range []string{"slug", "sandbox", "repo_dir", "github_repo", "source_repo"} {
		order = append(order, key)
		if !strings.Contains(content, `  "`+key+`": `) {
			t.Fatalf("task.json lacks %s:\n%s", key, content)
		}
	}
	if !strings.HasPrefix(content, "{\n  \"slug\": \"fix\",") || !strings.HasSuffix(content, "}\n") {
		t.Fatalf("task.json = %s", content)
	}
}

func TestFlagsMayFollowPositionalArguments(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix", "--plan", "--cpus", "4")
	if !strings.Contains(testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml")), "sandboxOptions:\n  cpus: 4\n") {
		t.Fatal("--cpus after the positionals was ignored")
	}
	h.expect(0, "up", "--plan", h.remote.Checkout, "--memory=8g", "fix")
	if !strings.Contains(testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml")), `memory: "8g"`) {
		t.Fatal("--memory between the positionals was ignored")
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	h := setup(t)
	h.expect(2)
	h.expect(2, "launch")
	h.expect(2, "up", h.remote.Checkout)
	h.expectStderr("required: slug")
	h.expect(2, "up", h.remote.Checkout, "fix", "extra")
	h.expectStderr("unrecognized arguments: extra")
	h.expect(2, "up", h.remote.Checkout, "fix", "--cpus", "many")
	h.expect(2, "broker", "restart")
	h.expect(0, "--help")
}

func TestUpRefusesAGlobalPolicyWithAllowRulesUnlessOverridden(t *testing.T) {
	h := setup(t)
	h.sbx.SetPolicy(`{"rules": [{"id": "default-ai-services", "scope": "global", "decision": "allow", "resource_type": "network"}]}`)
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("not deny-all")
	if h.anyCall("env create") {
		t.Fatal("created despite the global policy")
	}
	h.expect(0, "up", h.remote.Checkout, "fix", "--allow-global-policy")
	if !strings.HasPrefix(h.sbx.LastCall(), "env create") {
		t.Fatalf("last call %q", h.sbx.LastCall())
	}
}

func TestUpRefusesAnExistingSandbox(t *testing.T) {
	h := setup(t)
	h.sbx.SetSandboxes("stage-fix\n")
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("already exists")
}

func TestUpRefusesAnSbxOlderThanTheFloor(t *testing.T) {
	h := setup(t)
	t.Setenv("FAKE_SBX_VERSION", "v0.50.2")
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("0.51.0 or later")
}

func TestUpRefusesBadSlugs(t *testing.T) {
	h := setup(t)
	for _, slug := range []string{"Fix", "_fix", "fix_login", strings.Repeat("a", 42)} {
		h.expect(1, "up", h.remote.Checkout, slug)
		h.expectStderr("slug must be")
	}
}

func TestPlanShowsTheSbxPlanWithoutCreatingOrCheckingPolicy(t *testing.T) {
	h := setup(t)
	h.sbx.SetPolicy(`{"rules": [{"scope": "global", "decision": "allow", "resource_type": "network"}]}`)
	h.expect(0, "up", h.remote.Checkout, "fix", "--plan")
	if got := h.sbx.LastCall(); got != "env plan "+h.task("fix") {
		t.Errorf("last call %q", got)
	}
	if h.anyCall("env create", "policy") {
		t.Error("plan created or read the policy")
	}
}

func TestUpWithAWSProfilesGrantsThisSandboxATokenAndStartsTheBroker(t *testing.T) {
	h := setup(t)
	pack := filepath.Join(t.TempDir(), "pack")
	testutil.WriteFile(t, filepath.Join(pack, "pack.toml"), `
name = "org"
[[repos]]
match = "acme/api"
aws = { profiles = ["dev", "staging"], region = "us-west-2" }
`)
	h.expect(0, "up", h.remote.Checkout, "fix", "--pack", pack)
	grants := filepath.Join(h.home, "broker", "grants")
	if got := testutil.ReadFile(t, filepath.Join(grants, "fix.profiles")); got != "dev\nstaging\n" {
		t.Errorf("profiles = %q", got)
	}
	env := testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml"))
	for _, fragment := range []string{`command: "/bin/cat ` + filepath.Join(grants, "fix.token") + `"`, `profiles: "dev staging"`, `region: "us-west-2"`, `"localhost"`} {
		if !strings.Contains(env, fragment) {
			t.Errorf("env lacks %s:\n%s", fragment, env)
		}
	}
	if len(h.brokerStarts) != 1 || h.brokerStarts[0] != h.home {
		t.Errorf("broker starts = %v", h.brokerStarts)
	}
}

func TestDownRemovesTheEnvRevokesTheGrantAndDeletesTheTask(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	grants := filepath.Join(h.home, "broker", "grants")
	testutil.WriteFile(t, filepath.Join(grants, "fix.token"), "t")
	testutil.WriteFile(t, filepath.Join(grants, "fix.profiles"), "dev\n")
	h.sbx.SetSandboxes("stage-fix\n")
	h.expect(0, "down", "fix")
	if got := h.sbx.LastCall(); got != "env rm --force --no-input "+h.task("fix") {
		t.Errorf("last call %q", got)
	}
	if _, err := os.Stat(h.task("fix")); !os.IsNotExist(err) {
		t.Error("task dir remains")
	}
	if entries, _ := os.ReadDir(grants); len(entries) != 0 {
		t.Errorf("grants remain: %v", entries)
	}
}

func TestDownWithoutAnEnvFileRemovesTheSandboxDirectly(t *testing.T) {
	h := setup(t)
	h.sbx.SetSandboxes("stage-fix\n")
	h.expect(0, "down", "fix")
	if got := h.sbx.LastCall(); got != "rm --force stage-fix" {
		t.Errorf("last call %q", got)
	}
}

func TestDownOfAnUnknownTaskStillCleansUpQuietly(t *testing.T) {
	h := setup(t)
	h.expect(0, "down", "gone")
	if h.anyCall("env rm", "rm") {
		t.Error("removed a sandbox that does not exist")
	}
}

func shippedTask(t *testing.T) (*harness, string) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	box := filepath.Join(t.TempDir(), "box")
	testutil.RunGit(t, h.remote.Root, "clone", "-q", h.remote.Checkout, box)
	testutil.WriteFile(t, filepath.Join(box, "change.txt"), "agent work\n")
	testutil.RunGit(t, box, "add", ".")
	testutil.RunGit(t, box, "commit", "-q", "-m", "Fix the login redirect")
	testutil.RunGit(t, filepath.Join(h.task("fix"), "src"), "remote", "add", "sandbox-stage-fix", box)
	return h, box
}

func TestShipPushesTheSandboxHeadToAnAgentBranch(t *testing.T) {
	h, box := shippedTask(t)
	sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
	h.expect(0, "ship", "fix")
	branch := "agent/fix/" + sha[:8]
	if pushed := testutil.RunGit(t, h.remote.Bare, "rev-parse", branch); pushed != sha {
		t.Errorf("pushed %s, want %s", pushed, sha)
	}
	if want := "pushed " + sha[:8] + " to acme/api " + branch; !strings.Contains(h.stdout.String(), want) {
		t.Errorf("stdout %q lacks %q", h.stdout.String(), want)
	}
}

func TestShipRefusesBranchesOutsideAgent(t *testing.T) {
	h, _ := shippedTask(t)
	for _, branch := range []string{"main", "agent/", "feature/agent/x"} {
		h.expect(1, "ship", "fix", "--branch", branch)
		h.expectStderr("must start with agent/")
	}
}

func TestShipNeedsTheSandboxRemote(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	h.expect(1, "ship", "fix")
	h.expectStderr("no sandbox-stage-fix remote")
}

func TestCommandsOnAnUnknownTaskAskForStageUp(t *testing.T) {
	h := setup(t)
	h.expect(1, "ship", "nope")
	h.expectStderr("no task nope; run stage up first")
}

func TestPRBodyDisclosesAIGenerationAndAppendsTheConfiguredFooter(t *testing.T) {
	body := PRBody("fix", "abc123", "[Robots on behalf of Someone]")
	for _, fragment := range []string{"AI coding agent", "LLM", "abc123"} {
		if !strings.Contains(body, fragment) {
			t.Errorf("body lacks %q", fragment)
		}
	}
	if !strings.HasSuffix(body, "\n[Robots on behalf of Someone]\n") {
		t.Errorf("body = %q", body)
	}
	if !strings.HasSuffix(PRBody("fix", "abc123", ""), "Commit: abc123\n") {
		t.Error("body without footer does not end with the commit")
	}
}

func TestExecRunsInTheRepositoryAsTheAgent(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	h.expect(0, "exec", "fix", "--", "git", "status")
	repoDir := config.Resolve(filepath.Join(h.task("fix"), "src"))
	if got := h.sbx.LastCall(); got != "exec -u agent -w "+repoDir+" stage-fix git status" {
		t.Errorf("last call %q", got)
	}
	h.expect(1, "exec", "fix", "--")
	h.expectStderr("usage: stage exec")
}

func TestListShowsOnlyScoringStageSandboxes(t *testing.T) {
	h := setup(t)
	h.sbx.SetSandboxes("stage-fix\nclaude-other\n")
	h.expect(0, "list")
	if got := h.stdout.String(); got != "SANDBOX   STATUS\nstage-fix   running\n" {
		t.Errorf("stdout %q", got)
	}
}

func TestTheRecordedRepoDirIsThePathSbxIsGivenEvenThroughASymlinkedHome(t *testing.T) {
	h := setup(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STAGE_HOME", filepath.Join(link, "home"))
	h.expect(0, "up", h.remote.Checkout, "fix")
	var task Task
	json.Unmarshal([]byte(testutil.ReadFile(t, filepath.Join(real, "home", "tasks", "fix", "task.json"))), &task)
	created := strings.TrimPrefix(h.sbx.LastCall(), "env create --force --no-input ")
	if task.RepoDir != filepath.Join(created, "src") {
		t.Fatalf("repo_dir %s, but sbx was given %s", task.RepoDir, created)
	}
}
