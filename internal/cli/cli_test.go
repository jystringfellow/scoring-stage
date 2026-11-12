package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/github/githubtest"
	"github.com/jystringfellow/scoring-stage/internal/kits"
	"github.com/jystringfellow/scoring-stage/internal/source"
	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

type harness struct {
	t              *testing.T
	home           string
	sbx            *testutil.FakeSbx
	remote         testutil.Remote
	stdout, stderr bytes.Buffer
	brokerStarts   []string
	managed        string
	credentials    string
	hostProfiles   []string
	gh             string
	github         *fakeGitHub
	githubApp      *githubtest.Fake
	terminal       bool
	stdinTerminal  bool
	stdin          string
	typed          string
}

func setup(t *testing.T) *harness {
	t.Helper()
	given := filepath.Join(t.TempDir(), "stage-home")
	h := &harness{t: t, home: config.Resolve(given)}
	t.Setenv("STAGE_HOME", given)
	sbxConfig := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", sbxConfig)
	h.credentials = filepath.Join(sbxConfig, "sbx", "credentials.yaml")
	h.sbx = testutil.NewFakeSbx(t)
	h.remote = testutil.GitHubRemote(t)
	h.managed = filepath.Join(t.TempDir(), "managed.toml")
	original := config.ManagedPaths
	config.ManagedPaths = func() []string { return []string{h.managed} }
	t.Cleanup(func() { config.ManagedPaths = original })
	writeGitHubSetup(t, given, "")
	h.github = newFakeGitHub(t)
	return h
}

const fineGrainedMode = "[github]\nmode = \"fine-grained\"\n"

func writeGitHubSetup(t *testing.T, home, config string) {
	t.Helper()
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), config+fineGrainedMode)
	writeToken(t, filepath.Join(home, "secrets", "github", "acme.token"), "github_pat_readonly\n")
}

func writeToken(t *testing.T, path, token string) {
	t.Helper()
	testutil.WriteFile(t, path, token)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) stage(args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	app := &App{
		Stdin:           strings.NewReader(h.stdin),
		Stdout:          &h.stdout,
		Stderr:          &h.stderr,
		FindGh:          func() string { return h.gh },
		BrokerPID:       func(string) int { return 0 },
		BrokerStart:     func(home string) error { h.brokerStarts = append(h.brokerStarts, home); return nil },
		IsTerminal:      func() bool { return h.terminal },
		StdinIsTerminal: func() bool { return h.stdinTerminal },
		ReadSecret:      func() ([]byte, error) { return []byte(h.typed), nil },
		GitHubAPI:       h.github.api(),
		HostAWSProfiles: func() ([]string, error) {
			return h.hostProfiles, nil
		},
	}
	if h.githubApp != nil {
		app.GitHub = h.githubApp.Client()
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

func (h *harness) anyCallContaining(fragment string) bool {
	for _, call := range h.sbx.Calls() {
		if strings.Contains(call, fragment) {
			return true
		}
	}
	return false
}

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
	book := filepath.Join(t.TempDir(), "book")
	testutil.WriteFile(t, filepath.Join(book, "book.toml"), `
name = "org"
[[repos]]
match = "acme/api"
aws = { profiles = ["dev", "staging"], region = "us-west-2" }
`)
	h.expect(0, "up", h.remote.Checkout, "fix", "--book", book)
	grants := filepath.Join(h.home, "broker", "grants")
	if got := testutil.ReadFile(t, filepath.Join(grants, "fix.profiles")); got != "dev\nstaging\n" {
		t.Errorf("profiles = %q", got)
	}
	env := testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml"))
	for _, fragment := range []string{`command: "/bin/cat ` + filepath.Join(grants, "fix.token") + `"`, `profiles: "dev staging"`, `region: "us-west-2"`, `"localhost:9911"`} {
		if !strings.Contains(env, fragment) {
			t.Errorf("env lacks %s:\n%s", fragment, env)
		}
	}
	if len(h.brokerStarts) != 1 || h.brokerStarts[0] != h.home {
		t.Errorf("broker starts = %v", h.brokerStarts)
	}
}

func TestUpWithAnAWSProfileFlagGrantsItAsTheDefaultForThisSandbox(t *testing.T) {
	h := setup(t)
	h.hostProfiles = []string{"dev", "staging"}
	h.expect(0, "up", h.remote.Checkout, "fix", "--aws-profile", "staging")
	if got := testutil.ReadFile(t, filepath.Join(h.home, "broker", "grants", "fix.profiles")); got != "staging\n" {
		t.Errorf("profiles = %q", got)
	}
	if env := testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml")); !strings.Contains(env, `default_profile: "staging"`) {
		t.Errorf("env lacks the default profile:\n%s", env)
	}
}

func TestUpRejectsAnAWSProfileFlagMissingFromTheHostConfig(t *testing.T) {
	h := setup(t)
	h.hostProfiles = []string{"dev"}
	h.expect(1, "up", h.remote.Checkout, "fix", "--aws-profile", "stagin")
	h.expectStderr("--aws-profile stagin is not a profile in your AWS config")
	if h.anyCall("env create") {
		t.Error("created a sandbox for an unknown profile")
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
	return h, h.sandboxCommit("fix")
}

func (h *harness) sandboxCommit(slug string) string {
	h.t.Helper()
	box := filepath.Join(h.t.TempDir(), "box")
	testutil.RunGit(h.t, h.remote.Root, "clone", "-q", h.remote.Checkout, box)
	testutil.WriteFile(h.t, filepath.Join(box, "change.txt"), "agent work\n")
	testutil.RunGit(h.t, box, "add", ".")
	testutil.RunGit(h.t, box, "commit", "-q", "-m", "Fix the login redirect")
	testutil.RunGit(h.t, filepath.Join(h.task(slug), "src"), "remote", "add", "sandbox-stage-"+slug, box)
	h.pinGitConfig(slug)
	return box
}

// pinGitConfig records the scratch clone's config as sbx leaves it, with the
// sandbox remote that the fake sbx does not add itself.
func (h *harness) pinGitConfig(slug string) {
	h.t.Helper()
	task, err := loadTask(h.home, slug)
	if err != nil {
		h.t.Fatal(err)
	}
	if task.GitConfig, err = source.ConfigFingerprint(task.RepoDir, task.Sandbox); err != nil {
		h.t.Fatal(err)
	}
	if err := writeTask(*task, h.task(slug)); err != nil {
		h.t.Fatal(err)
	}
}

func TestShipPushesTheSandboxHeadToAnAgentBranch(t *testing.T) {
	h, box := shippedTask(t)
	sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
	h.expect(0, "ship", "fix", "--yes")
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

func TestShipPushesUnderTheConfiguredBranchPrefixAndRefusesBranchesOutsideIt(t *testing.T) {
	h, box := shippedTask(t)
	writeGitHubSetup(t, h.home, "[ship]\nbranch_prefix = \"bot/\"\n")
	sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
	main := testutil.RunGit(t, h.remote.Bare, "rev-parse", "main")
	for _, branch := range []string{"main", "bot/", "agent/fix/x", "feature/bot/x"} {
		h.expect(1, "ship", "fix", "--yes", "--branch", branch)
		h.expectStderr("must start with bot/")
	}
	h.expect(1, "ship", "fix", "--yes", "--branch", "bot/fix/\u202eniam")
	h.expectStderr("control or invisible characters")
	h.expect(0, "ship", "fix", "--yes")
	if pushed := testutil.RunGit(t, h.remote.Bare, "rev-parse", "bot/fix/"+sha[:8]); pushed != sha {
		t.Errorf("pushed %s, want %s", pushed, sha)
	}
	h.expect(0, "ship", "fix", "--yes", "--branch", "bot/fix/again")
	if out := testutil.RunGit(t, h.remote.Bare, "branch", "--list", "agent/*"); out != "" {
		t.Errorf("pushed outside the configured prefix: %s", out)
	}
	if now := testutil.RunGit(t, h.remote.Bare, "rev-parse", "main"); now != main {
		t.Errorf("main moved from %s to %s", main, now)
	}
}

func TestShipRefusesABranchPrefixThatIsNotANamespaceBeforePushing(t *testing.T) {
	h, _ := shippedTask(t)
	main := testutil.RunGit(t, h.remote.Bare, "rev-parse", "main")
	writeGitHubSetup(t, h.home, "[ship]\nbranch_prefix = \"ma\"\n")
	h.expect(1, "ship", "fix", "--yes", "--branch", "main")
	h.expectStderr("branch_prefix must end in /")
	if now := testutil.RunGit(t, h.remote.Bare, "rev-parse", "main"); now != main {
		t.Fatalf("main moved from %s to %s", main, now)
	}
}

func TestShipPushesNothingWhenTheUserConfigCannotBeLoaded(t *testing.T) {
	h, _ := shippedTask(t)
	writeGitHubSetup(t, h.home, "[aws]\nregion = 1\n")
	h.expect(1, "ship", "fix", "--yes")
	h.expectStderr("config.toml")
	if out := testutil.RunGit(t, h.remote.Bare, "branch", "--list", "agent/*"); out != "" {
		t.Fatalf("pushed with a config that does not load: %s", out)
	}
	if ships := h.recorded("fix").Ships; len(ships) != 0 {
		t.Errorf("recorded %+v", ships)
	}
}

func TestShipRefusesARenameThatMovesAProtectedFileAway(t *testing.T) {
	h := setup(t)
	h.commitToMain(map[string]string{".envrc": "export PATH_add=bin\nexport TOOLS=one two three\n"})
	h.expect(0, "up", h.remote.Checkout, "fix")
	box := filepath.Join(t.TempDir(), "box")
	testutil.RunGit(t, h.remote.Root, "clone", "-q", h.remote.Checkout, box)
	testutil.RunGit(t, box, "mv", ".envrc", "notes.txt")
	testutil.RunGit(t, box, "commit", "-q", "-m", "Move the environment file")
	testutil.RunGit(t, filepath.Join(h.task("fix"), "src"), "remote", "add", "sandbox-stage-fix", box)
	h.pinGitConfig("fix")
	h.expect(1, "ship", "fix", "--yes")
	h.expectStderr("refusing to ship changes to CI, workflow or sandbox config files: .envrc")
}

func TestShipRefusesToRunOnAConfigWithAnEmptyBranchPrefix(t *testing.T) {
	h, _ := shippedTask(t)
	writeGitHubSetup(t, h.home, "[ship]\nbranch_prefix = \"\"\n")
	h.expect(1, "ship", "fix", "--yes", "--branch", "main")
	h.expectStderr("branch_prefix cannot be empty")
	if out := testutil.RunGit(t, h.remote.Bare, "log", "--format=%s", "-1", "main"); strings.Contains(out, "Fix the login redirect") {
		t.Fatalf("pushed to main: %s", out)
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
	body := PRBody("fix", "abc123", config.DefaultPRBody, "[Robots on behalf of Someone]")
	for _, fragment := range []string{"AI coding agent", "LLM", "not yet been reviewed by a human", "abc123"} {
		if !strings.Contains(body, fragment) {
			t.Errorf("body lacks %q", fragment)
		}
	}
	if !strings.HasSuffix(body, "\n[Robots on behalf of Someone]\n") {
		t.Errorf("body = %q", body)
	}
	if !strings.HasSuffix(PRBody("fix", "abc123", config.DefaultPRBody, ""), "Commit: abc123\n") {
		t.Error("body without footer does not end with the commit")
	}
}

func TestPRBodySaysAnAIAgentProducedTheChangeWhateverBodyIsConfigured(t *testing.T) {
	for _, configured := range []string{"", "Review against the team checklist.", "Written by hand."} {
		body := PRBody("fix", "abc123", configured, "")
		if !strings.HasPrefix(body, "This draft pull request was produced by an AI coding agent running in a scoring-stage sandbox (task `fix`)") {
			t.Errorf("with %q configured, body = %q", configured, body)
		}
		if !strings.Contains(body, "Commit: abc123") {
			t.Errorf("with %q configured, body lacks the commit: %q", configured, body)
		}
		if strings.Contains(body, "not yet been reviewed by a human") {
			t.Errorf("with %q configured, body keeps the default text: %q", configured, body)
		}
		if configured != "" && !strings.Contains(body, "\n\n"+configured+"\n\n") {
			t.Errorf("body lacks the configured text as its own paragraph: %q", body)
		}
	}
}

func TestPRBodyTrimsTheFooterAndLeavesOutAnEmptyOne(t *testing.T) {
	if body := PRBody("fix", "abc123", "", " \n"); !strings.HasSuffix(body, "Commit: abc123\n") {
		t.Errorf("body = %q", body)
	}
	if body := PRBody("fix", "abc123", "", "\n Sent by a robot. \n\n"); !strings.HasSuffix(body, "Commit: abc123\n\nSent by a robot.\n") {
		t.Errorf("body = %q", body)
	}
}

func TestShipWithPRWritesTheConfiguredBodyAndFooter(t *testing.T) {
	h, _ := shippedTask(t)
	writeGitHubSetup(t, h.home, "[ship]\npr_body = \"Review against the team checklist.\"\npr_footer = \"Sent by a robot.\"\n")
	log := h.ghAnswering("https://github.com/acme/api/pull/7\n", 0)
	h.expect(0, "ship", "fix", "--yes", "--pr")
	call := testutil.ReadFile(t, log)
	for _, want := range []string{"produced by an AI coding agent", "Review against the team checklist.", "Sent by a robot."} {
		if !strings.Contains(call, want) {
			t.Errorf("gh was asked %q, which lacks %q", call, want)
		}
	}
	if strings.Contains(call, "not yet been reviewed by a human") {
		t.Errorf("gh was asked %q, which keeps the default text", call)
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
	writeGitHubSetup(t, filepath.Join(real, "home"), "")
	h.expect(0, "up", h.remote.Checkout, "fix")
	var task Task
	json.Unmarshal([]byte(testutil.ReadFile(t, filepath.Join(real, "home", "tasks", "fix", "task.json"))), &task)
	created := strings.TrimPrefix(h.sbx.LastCall(), "env create --force --no-input ")
	if task.RepoDir != filepath.Join(created, "src") {
		t.Fatalf("repo_dir %s, but sbx was given %s", task.RepoDir, created)
	}
}

func TestUpWithoutAgentsWarnsThatTheBoxHasOnlyAShell(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	h.expectStderr("no agents chosen")
	if env := testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml")); strings.Contains(env, "-mixin") {
		t.Fatalf("agent kits without a choice:\n%s", env)
	}
}

func TestAgentsComeFromTheFlagAndTheRepositorysCommittedConfig(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, filepath.Join(h.remote.Checkout, ".scoring-stage", "config.toml"), "agents = [\"codex\"]\n")
	testutil.RunGit(t, h.remote.Checkout, "add", ".")
	testutil.RunGit(t, h.remote.Checkout, "commit", "-q", "-m", "Add scoring-stage config")
	testutil.RunGit(t, h.remote.Checkout, "push", "-q", "https://github.com/acme/api.git", "HEAD:main")
	testutil.WriteFile(t, filepath.Join(h.remote.Checkout, ".scoring-stage", "config.toml"), "agents = [\"copilot\"]\n")
	h.expect(0, "up", h.remote.Checkout, "fix", "--agent", "agy")
	env := testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml"))
	for _, want := range []string{"codex-mixin", "antigravity-mixin", "openai:"} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %s", want)
		}
	}
	if strings.Contains(env, "copilot") {
		t.Error("used the uncommitted repo config")
	}
	if !strings.Contains(h.stdout.String(), "kits: shell, codex-mixin, antigravity-mixin, stage") {
		t.Errorf("stdout %q", h.stdout.String())
	}
}

func TestUpRejectsAnUnknownAgent(t *testing.T) {
	h := setup(t)
	h.expect(1, "up", h.remote.Checkout, "fix", "--agent", "gemeni")
	h.expectStderr("unknown agent gemeni")
}

func TestSetupSavesAgents(t *testing.T) {
	h := setup(t)
	h.expect(0, "setup", "--agent", "claude", "--agent", "codex")
	if got := testutil.ReadFile(t, filepath.Join(h.home, "config.toml")); got != "agents = [\"claude\", \"codex\"]\n"+fineGrainedMode {
		t.Fatalf("config.toml = %q", got)
	}
	if !strings.Contains(h.stdout.String(), "stage: agents: claude, codex") {
		t.Errorf("stdout %q", h.stdout.String())
	}
	h.expect(0, "setup")
	h.expect(1, "setup", "--agent", "agy")
	h.expectStderr("already sets agents")
}

func TestCopiedLoginsAreSavedOnTheHostAndCopiedIntoNewSandboxes(t *testing.T) {
	h := setup(t)
	writeGitHubSetup(t, h.home, "copy_logins = [\"agy\"]\n")
	saved := filepath.Join(h.home, "logins", "agy", "antigravity-oauth-token")
	token := h.sbx.BoxFile("antigravity-oauth-token")

	h.expect(0, "up", h.remote.Checkout, "first", "--agent", "agy")
	if !strings.Contains(h.stdout.String(), "no saved agy login yet") {
		t.Fatalf("stdout %q", h.stdout.String())
	}
	h.expect(1, "login", "first")
	h.expectStderr("stage-first has no agy login yet")

	testutil.WriteFile(t, token, "token-1")
	h.expect(0, "login", "first")
	if got := testutil.ReadFile(t, saved); got != "token-1" {
		t.Fatalf("saved %q", got)
	}
	for path, mode := range map[string]os.FileMode{saved: 0o600, filepath.Dir(saved): 0o700} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != mode {
			t.Errorf("%s mode %v, want %v", path, info.Mode().Perm(), mode)
		}
	}
	if strings.Contains(h.stdout.String()+h.stderr.String(), "token-1") {
		t.Error("the token was printed")
	}

	os.Remove(token)
	h.expect(0, "up", h.remote.Checkout, "second", "--agent", "agy")
	if got := testutil.ReadFile(t, token); got != "token-1" {
		t.Fatalf("restored %q", got)
	}
	if want := "exec -i -u agent stage-second sh -c"; !h.anyCall(want) {
		t.Errorf("no %q call in %v", want, h.sbx.Calls())
	}

	testutil.WriteFile(t, token, "token-2")
	h.sbx.SetSandboxes("stage-second\n")
	h.expect(0, "down", "second")
	if got := testutil.ReadFile(t, saved); got != "token-2" {
		t.Fatalf("down saved %q", got)
	}
}

func TestAgysCompletedSetupTravelsWithItsLoginWhenThereIsOne(t *testing.T) {
	h := setup(t)
	writeGitHubSetup(t, h.home, "copy_logins = [\"agy\"]\n")
	savedSetup := filepath.Join(h.home, "logins", "agy", "onboarding.json")
	setupFile := h.sbx.BoxFile("onboarding.json")

	h.expect(0, "up", h.remote.Checkout, "first", "--agent", "agy")
	testutil.WriteFile(t, h.sbx.BoxFile("antigravity-oauth-token"), "token-1")
	h.expect(0, "login", "first")
	if _, err := os.Stat(savedSetup); !os.IsNotExist(err) {
		t.Fatal("saved a setup file the sandbox did not have")
	}

	testutil.WriteFile(t, setupFile, `{"onboardingComplete": true}`)
	h.expect(0, "login", "first")
	if got := testutil.ReadFile(t, savedSetup); got != `{"onboardingComplete": true}` {
		t.Fatalf("saved setup %q", got)
	}

	os.Remove(setupFile)
	h.expect(0, "up", h.remote.Checkout, "second", "--agent", "agy")
	if got := testutil.ReadFile(t, setupFile); got != `{"onboardingComplete": true}` {
		t.Fatalf("restored setup %q", got)
	}
	if want := "/home/agent/.gemini/antigravity-cli/cache/onboarding.json"; !h.anyCallContaining(want) {
		t.Errorf("no call writing %s in %v", want, h.sbx.Calls())
	}
}

func TestLoginsAreLeftAloneUnlessCopyLoginsAsksForThem(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix", "--agent", "agy")
	h.sbx.SetSandboxes("stage-fix\n")
	h.expect(0, "down", "fix")
	if h.anyCall("exec") {
		t.Errorf("touched logins without copy_logins: %v", h.sbx.Calls())
	}
	h.sbx.SetSandboxes("")
	h.expect(0, "up", h.remote.Checkout, "fix", "--agent", "agy")
	h.expect(1, "login", "fix")
	h.expectStderr("no agent listed in copy_logins")
}

func TestTheTaskRecordsItsAgents(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix", "--agent", "agy", "--agent", "claude")
	var task Task
	json.Unmarshal([]byte(testutil.ReadFile(t, filepath.Join(h.task("fix"), "task.json"))), &task)
	if strings.Join(task.Agents, " ") != "agy claude" {
		t.Fatalf("agents = %v", task.Agents)
	}
}

func (h *harness) commitToMain(files map[string]string) {
	h.t.Helper()
	for path, content := range files {
		testutil.WriteFile(h.t, filepath.Join(h.remote.Checkout, path), content)
	}
	testutil.RunGit(h.t, h.remote.Checkout, "add", ".")
	testutil.RunGit(h.t, h.remote.Checkout, "commit", "-q", "-m", "Change the repository")
	testutil.RunGit(h.t, h.remote.Checkout, "push", "-q", "https://github.com/acme/api.git", "HEAD:main")
}

func TestARepositoryKitNeedsApprovalUntilItChanges(t *testing.T) {
	h := setup(t)
	h.commitToMain(map[string]string{
		".scoring-stage/config.toml":         "kits = [\"./.scoring-stage/kits/rules\"]\n",
		".scoring-stage/kits/rules/kit.yaml": "allow: [example.com]\n",
	})
	h.expect(1, "up", h.remote.Checkout, "one")
	h.expectStderr("asks for kits, books or takes you have not approved")
	h.expectStderr("not approved; review them")
	if h.anyCall("env create") {
		t.Fatal("created a sandbox with an unapproved repository kit")
	}

	h.expect(0, "up", h.remote.Checkout, "two", "--approve-repo-config")
	h.expect(0, "up", h.remote.Checkout, "three")

	h.commitToMain(map[string]string{".scoring-stage/kits/rules/kit.yaml": "allow: [\"**\"]\n"})
	h.expect(1, "up", h.remote.Checkout, "four")
	h.expectStderr("asks for kits, books or takes you have not approved")
}

func boxCommit(t *testing.T, box, path, message string) {
	t.Helper()
	testutil.WriteFile(t, filepath.Join(box, path), "agent work\n")
	testutil.RunGit(t, box, "add", ".")
	testutil.RunGit(t, box, "commit", "-q", "-m", message)
}

func TestShipShowsWhatItWillPushAndWaitsForAYes(t *testing.T) {
	h, box := shippedTask(t)
	sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
	h.expect(1, "ship", "fix")
	for _, want := range []string{"shipping to acme/api agent/fix/" + sha[:8], "Fix the login redirect", "change.txt | 1 +"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, h.stdout.String())
		}
	}
	h.expectStderr("not shipped; rerun with --yes")
	if out := testutil.RunGit(t, h.remote.Bare, "branch", "--list", "agent/*"); out != "" {
		t.Fatalf("pushed without a yes: %s", out)
	}
}

func TestShipRefusesProtectedPathsUnlessAllowed(t *testing.T) {
	for _, path := range []string{".github/workflows/ci.yml", "azure-pipelines.yml", "build/azure-pipelines-pr.yaml", ".scoring-stage/config.toml", "Jenkinsfile", ".vscode/tasks.json", ".devcontainer/devcontainer.json", ".envrc"} {
		h, box := shippedTask(t)
		boxCommit(t, box, path, "Touch "+path)
		h.expect(1, "ship", "fix", "--yes")
		h.expectStderr("refusing to ship changes to CI, workflow or sandbox config files: " + path)
		h.expect(0, "ship", "fix", "--yes", "--allow-protected-paths")
		h.expectStderr("shipping changes to protected files: " + path)
	}
}

func TestShipRefusesPathsTheUserConfigAddsToTheProtectedList(t *testing.T) {
	for _, path := range []string{"deploy/prod.yaml", "Makefile", ".github/workflows/ci.yml"} {
		h, box := shippedTask(t)
		writeGitHubSetup(t, h.home, "[ship]\nprotected_paths = [\"deploy/\", \"Makefile\"]\n")
		boxCommit(t, box, path, "Touch "+path)
		h.expect(1, "ship", "fix", "--yes")
		h.expectStderr("refusing to ship changes to CI, workflow or sandbox config files: " + path)
		h.expect(0, "ship", "fix", "--yes", "--allow-protected-paths")
		h.expectStderr("shipping changes to protected files: " + path)
	}
	h, box := shippedTask(t)
	writeGitHubSetup(t, h.home, "[ship]\nprotected_paths = [\"deploy/\", \"Makefile\"]\n")
	boxCommit(t, box, "docs/deploy/Makefile.md", "Describe the deploy")
	h.expect(0, "ship", "fix", "--yes")
}

func TestShipWarnsAboutCommitsWithoutAnAgentTrailerWhenTheUserConfigAsks(t *testing.T) {
	h, box := shippedTask(t)
	writeGitHubSetup(t, h.home, "[ship]\nwarn_missing_trailer = true\n")
	boxCommit(t, box, "two.txt", "Second change\n\nCo-authored-by: Claude <noreply@anthropic.com>")
	h.expect(0, "ship", "fix", "--yes")
	first := testutil.RunGit(t, box, "rev-parse", "--short", "HEAD~1")
	h.expectStderr("commits without a Co-authored-by trailer naming the agent: " + first)
	if second := testutil.RunGit(t, box, "rev-parse", "--short", "HEAD"); strings.Contains(h.stderr.String(), second) {
		t.Errorf("warned about the attributed commit %s", second)
	}
}

func TestShipSaysNothingAboutTrailersByDefault(t *testing.T) {
	h, _ := shippedTask(t)
	h.expect(0, "ship", "fix", "--yes")
	if strings.Contains(h.stderr.String(), "Co-authored-by") {
		t.Errorf("stderr %q", h.stderr.String())
	}
}

func TestSetupMentionsCopilotAndItsGhSecretOnlyWhenCopilotIsAConfiguredAgent(t *testing.T) {
	for _, gh := range []int{0, 1} {
		h := setup(t)
		h.gh = fakeGh(t, gh)
		writeGitHubSetup(t, h.home, "agents = [\"claude\"]\n")
		h.expect(0, "setup")
		said := h.stdout.String() + h.stderr.String()
		for _, unwanted := range []string{"gh auth:", "copilot secret", "Copilot will"} {
			if strings.Contains(said, unwanted) {
				t.Errorf("setup without copilot, gh exiting %d, said %q in: %s", gh, unwanted, said)
			}
		}
	}
	h := setup(t)
	h.gh = fakeGh(t, 0)
	writeGitHubSetup(t, h.home, "agents = [\"claude\", \"copilot\"]\n")
	h.expect(0, "setup")
	if !strings.Contains(h.stdout.String(), "gh auth: ok (source of the per-box copilot secret)") {
		t.Errorf("stdout %q", h.stdout.String())
	}
	h.gh = fakeGh(t, 1)
	h.expect(0, "setup")
	h.expectStderr("gh is missing or not logged in; Copilot will be unauthenticated")
	h.gh = ""
	h.expect(0, "setup")
	h.expectStderr("gh is missing or not logged in; Copilot will be unauthenticated")
}

func TestShipRefusesWhenTheSandboxHasNothingNew(t *testing.T) {
	h, box := shippedTask(t)
	testutil.RunGit(t, box, "reset", "-q", "--hard", "HEAD~1")
	h.expect(1, "ship", "fix", "--yes")
	h.expectStderr("nothing to ship")
}

func TestHostGitIgnoresHooksAndFsmonitorInTheScratchClone(t *testing.T) {
	h, _ := shippedTask(t)
	scratch := filepath.Join(h.task("fix"), "src")
	marker := filepath.Join(t.TempDir(), "ran")
	hook := testutil.WriteFile(t, filepath.Join(scratch, ".git", "hooks", "pre-push"), "#!/bin/sh\ntouch "+marker+"\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, scratch, "config", "core.fsmonitor", "touch "+marker+"; false")
	h.pinGitConfig("fix")
	h.expect(0, "ship", "fix", "--yes")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("host git ran a command from the scratch clone's config")
	}
}

func TestShipRefusesAScratchCloneWhoseGitConfigChanged(t *testing.T) {
	h, _ := shippedTask(t)
	scratch := filepath.Join(h.task("fix"), "src")
	testutil.RunGit(t, scratch, "config", "url.ssh://evil/.insteadOf", "https://github.com/")
	h.expect(1, "ship", "fix", "--yes")
	h.expectStderr("the git config in " + scratch + " changed after stage up")
	if out := testutil.RunGit(t, h.remote.Bare, "branch", "--list", "agent/*"); out != "" {
		t.Fatalf("pushed from a changed clone: %s", out)
	}
}

func TestShipAllowsSbxToMoveTheSandboxRemoteWhenTheBoxRestarts(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	scratch := filepath.Join(h.task("fix"), "src")
	task, err := loadTask(h.home, "fix")
	if err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, scratch, "remote", "add", "sandbox-stage-fix", "git://127.0.0.1:49207/src")
	testutil.RunGit(t, scratch, "config", "--add", "remote.sandbox-stage-fix.fetch", "+refs/heads/*:refs/sandboxes/stage-fix/*")
	if err := checkGitConfig(task); err != nil {
		t.Fatalf("refused the remote sbx added on start: %v", err)
	}
	testutil.RunGit(t, scratch, "remote", "set-url", "sandbox-stage-fix", "git://127.0.0.1:49208/src")
	if err := checkGitConfig(task); err != nil {
		t.Fatalf("refused the port sbx chose on restart: %v", err)
	}
	testutil.RunGit(t, scratch, "remote", "remove", "sandbox-stage-fix")
	if err := checkGitConfig(task); err != nil {
		t.Fatalf("refused the config sbx left on stop: %v", err)
	}
	testutil.RunGit(t, scratch, "remote", "add", "sandbox-stage-fix", "git://127.0.0.1:49209/src")
	testutil.RunGit(t, scratch, "config", "remote.sandbox-stage-fix.uploadpack", "touch /tmp/x; git-upload-pack")
	if err := checkGitConfig(task); err == nil {
		t.Fatal("allowed a sandbox remote sbx did not write")
	}
}

func TestShipAcceptsATaskRecordedWithTheWholeConfigWhileItIsUnchanged(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	scratch := filepath.Join(h.task("fix"), "src")
	testutil.RunGit(t, scratch, "remote", "add", "sandbox-stage-fix", "git://127.0.0.1:49207/src")
	testutil.RunGit(t, scratch, "config", "--add", "remote.sandbox-stage-fix.fetch", "+refs/heads/*:refs/sandboxes/stage-fix/*")
	task, err := loadTask(h.home, "fix")
	if err != nil {
		t.Fatal(err)
	}
	if task.GitConfig, err = source.ConfigFingerprint(task.RepoDir, ""); err != nil {
		t.Fatal(err)
	}
	if err := checkGitConfig(task); err != nil {
		t.Fatalf("refused an unchanged config: %v", err)
	}
	testutil.RunGit(t, scratch, "config", "core.sshCommand", "touch /tmp/x")
	if err := checkGitConfig(task); err == nil {
		t.Fatal("allowed a changed config")
	}
}

func TestUpRecordsTheScratchClonesGitConfig(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	task, err := loadTask(h.home, "fix")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := source.ConfigFingerprint(task.RepoDir, task.Sandbox)
	if task.GitConfig == "" || task.GitConfig != want {
		t.Fatalf("git_config %q, want %q", task.GitConfig, want)
	}
}

func TestTheShipSummaryEscapesTerminalControlCharacters(t *testing.T) {
	h, box := shippedTask(t)
	boxCommit(t, box, "two.txt", "Harmless\x1b[2A\x1b]52;c;ZXZpbA==\x07 subject \u202egnp.exe")
	h.expect(0, "ship", "fix", "--yes")
	out := h.stdout.String()
	if strings.ContainsAny(out, "\x1b\x07\u202e") {
		t.Fatalf("summary carries control characters: %q", out)
	}
	if !strings.Contains(out, `Harmless\x1b[2A\x1b]52;c;ZXZpbA==\x07 subject`) {
		t.Errorf("summary %q lacks the escaped subject", out)
	}
}

func TestManagedSettingsCanRefuseTheGlobalPolicyOverrideAndShowInSetup(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, h.managed, "allow_global_policy = false\nallowed_agents = [\"claude\"]\n")
	h.sbx.SetPolicy(`{"rules": [{"id": "r1", "scope": "global", "decision": "allow", "resource_type": "network"}]}`)
	h.expect(1, "up", h.remote.Checkout, "fix", "--allow-global-policy")
	h.expectStderr("--allow-global-policy is refused by the managed settings (" + h.managed + ")")
	h.expect(0, "setup")
	for _, want := range []string{"managed settings: " + h.managed, "allowed agents: claude", "--allow-global-policy: refused"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("setup output lacks %q:\n%s", want, h.stdout.String())
		}
	}
}

func TestShipRefusesAScratchCloneWhoseConfigIncludesOtherFiles(t *testing.T) {
	h, _ := shippedTask(t)
	scratch := filepath.Join(h.task("fix"), "src")
	testutil.RunGit(t, scratch, "config", "include.path", "/tmp/elsewhere")
	h.pinGitConfig("fix")
	h.expect(1, "ship", "fix", "--yes")
	h.expectStderr("includes other files (include.path)")
}

func TestUpRefusesUntilAGitHubModeIsChosen(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), "")
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("choose how sandboxes reach GitHub: run `stage setup --github MODE`")
	if h.anyCall("env create") {
		t.Error("created a sandbox before a GitHub mode was chosen")
	}
	h.expect(0, "setup", "--github", "none")
	h.expect(0, "up", h.remote.Checkout, "fix")
}

func TestSetupSavesTheGitHubModeOnceAndReportsAMissingChoice(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), "agents = [\"codex\"]")
	h.expect(0, "setup")
	h.expectStderr("choose how sandboxes reach GitHub")
	h.expect(0, "setup", "--github", "fine-grained")
	if got := testutil.ReadFile(t, filepath.Join(h.home, "config.toml")); got != "agents = [\"codex\"]\n\n"+fineGrainedMode {
		t.Fatalf("config.toml = %q", got)
	}
	if !strings.Contains(h.stdout.String(), "github token for acme: fine-grained") {
		t.Errorf("setup did not report the acme token: %s", h.stdout.String())
	}
	h.expect(1, "setup", "--github", "gh")
	h.expectStderr("already sets github; edit it there")
	h.expect(1, "setup", "--github", "bogus")
	h.expectStderr("--github must be one of fine-grained, app, gh, none")
	writeToken(t, filepath.Join(h.home, "secrets", "github", "Globex.token"), "github_pat_globex\n")
	h.expect(0, "setup")
	if !strings.Contains(h.stdout.String(), "github token for globex: fine-grained") {
		t.Errorf("setup did not find Globex.token under its lowercased owner: %s\n%s", h.stdout.String(), h.stderr.String())
	}
}

func TestUpGivesTheSandboxOnlyTheTokenForItsRepositorysOwner(t *testing.T) {
	h := setup(t)
	writeToken(t, filepath.Join(h.home, "secrets", "github", "globex.token"), "github_pat_globex\n")
	h.expect(0, "up", h.remote.Checkout, "fix")
	env := testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml"))
	if !strings.Contains(env, filepath.Join(h.home, "secrets", "github", "acme.token")) || strings.Contains(env, "globex") {
		t.Errorf("env does not hold exactly acme's token:\n%s", env)
	}
}

func TestUpRefusesAnOwnerWithoutAToken(t *testing.T) {
	h := setup(t)
	if err := os.Rename(filepath.Join(h.home, "secrets", "github", "acme.token"), filepath.Join(h.home, "secrets", "github", "globex.token")); err != nil {
		t.Fatal(err)
	}
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("no GitHub token for acme at " + filepath.Join(h.home, "secrets", "github", "acme.token"))
	h.expectStderr("A fine-grained token covers one owner")
	if h.anyCall("env create") {
		t.Error("created a sandbox without a GitHub token")
	}
}

func TestUpPointsTheOldSingleTokenFileAtItsNewPlace(t *testing.T) {
	h := setup(t)
	if err := os.Remove(filepath.Join(h.home, "secrets", "github", "acme.token")); err != nil {
		t.Fatal(err)
	}
	writeToken(t, filepath.Join(h.home, "secrets", "github-readonly-token"), "github_pat_readonly\n")
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("If " + filepath.Join(h.home, "secrets", "github-readonly-token") + " is a token for acme, move it there")
	h.expect(0, "setup")
	h.expectStderr("github-readonly-token is no longer used")
}

func TestUpRefusesAGitHubTokenThatCanWrite(t *testing.T) {
	h := setup(t)
	writeToken(t, filepath.Join(h.home, "secrets", "github", "acme.token"), "gho_fromghauth\n")
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("gh CLI login token (gho_)")
}

func TestUpWithGitHubNoneGivesTheSandboxNoGitHubCredential(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), "[github]\nmode = \"none\"\n")
	h.expect(0, "up", h.remote.Checkout, "fix")
	if env := testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml")); strings.Contains(env, "github") {
		t.Errorf("env mentions github:\n%s", env)
	}
}

func TestUpInGhModeUsesAPerSandboxGhSecretAndWarnsThatItCanWrite(t *testing.T) {
	h := setup(t)
	gh := fakeGh(t, 0)
	h.gh = gh
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), "[github]\nmode = \"gh\"\n")
	h.expect(0, "up", h.remote.Checkout, "fix")
	h.expectStderr("commit to and move or delete branches, open and merge pull requests")
	env := testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml"))
	if !strings.Contains(env, gh+" auth token") || !strings.Contains(env, "api.github.com") {
		t.Errorf("env has no gh secret and binding:\n%s", env)
	}
	h.expect(0, "setup")
	h.expectStderr("The proxy refuses only git push and REST writes")
}

func TestManagedSettingsRefuseGhModeUntilAllowedModesListsIt(t *testing.T) {
	h := setup(t)
	h.gh = fakeGh(t, 0)
	testutil.WriteFile(t, h.managed, "repo_kits = \"ask\"\n")
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), "[github]\nmode = \"gh\"\n")
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr(`GitHub mode "gh" is not allowed by ` + h.managed + `: managed settings allow a mode outside "fine-grained", "none" only when [github] allowed_modes lists it`)
	if h.anyCall("env create") {
		t.Error("created a gh-mode sandbox under managed settings")
	}
	h.expect(0, "setup")
	h.expectStderr("stage up will refuse")
	if !strings.Contains(h.stdout.String(), "allowed GitHub modes: fine-grained, none (any mode outside fine-grained and none needs listing in [github] allowed_modes)") {
		t.Errorf("setup output lacks the allowed GitHub modes:\n%s", h.stdout.String())
	}
	testutil.WriteFile(t, h.managed, "[github]\nallowed_modes = [\"gh\", \"none\"]\n")
	h.expect(0, "up", h.remote.Checkout, "fix")
	h.expectStderr("The proxy refuses only git push and REST writes")
}

func TestManagedAllowedGitHubModesRefuseOtherModesOnUpAndSetup(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, h.managed, "[github]\nallowed_modes = [\"gh\", \"none\"]\n")
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr(`GitHub mode "fine-grained" is not allowed by ` + h.managed + ` (allowed: "gh", "none"); set [github] mode in`)
	if h.anyCall("env create") {
		t.Error("created a sandbox in a mode the managed settings refuse")
	}
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), "")
	h.expect(1, "setup", "--agent", "codex", "--github", "fine-grained")
	h.expectStderr(`--github: GitHub mode "fine-grained" is not allowed by`)
	if got := testutil.ReadFile(t, filepath.Join(h.home, "config.toml")); got != "" {
		t.Fatalf("config.toml = %q", got)
	}
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("(gh: your gh login, which can write; none: no GitHub credential)")
	if strings.Contains(h.stderr.String(), "fine-grained") {
		t.Errorf("the no-mode error offers a refused mode: %s", h.stderr.String())
	}
	h.expect(0, "setup", "--github", "none")
	h.expect(0, "up", h.remote.Checkout, "fix")
}

func TestASingleManagedGitHubModeIsTheDefault(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, h.managed, "[github]\nallowed_modes = [\"none\"]\n")
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), "")
	h.expect(0, "setup")
	if !strings.Contains(h.stdout.String(), "github: none") {
		t.Errorf("setup did not use the managed default:\n%s\n%s", h.stdout.String(), h.stderr.String())
	}
	h.expect(0, "up", h.remote.Checkout, "fix")
	if env := testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml")); strings.Contains(env, "github") {
		t.Errorf("env mentions github:\n%s", env)
	}
}

func TestAnEmptyManagedGitHubModeListRefusesEveryMode(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, h.managed, "[github]\nallowed_modes = []\n")
	for _, config := range []string{"", "[github]\nmode = \"none\"\n"} {
		testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), config)
		h.expect(1, "up", h.remote.Checkout, "fix")
		h.expectStderr("sets [github] allowed_modes = [], so no GitHub mode is allowed")
	}
	h.expect(1, "setup", "--github", "none")
	h.expectStderr("no GitHub mode is allowed")
	if h.anyCall("env create") {
		t.Error("created a sandbox although no GitHub mode is allowed")
	}
}

func TestSetupRejectsABadGitHubModeBeforeSavingAgents(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), "")
	h.expect(1, "setup", "--agent", "codex", "--github", "bogus")
	if got := testutil.ReadFile(t, filepath.Join(h.home, "config.toml")); got != "" {
		t.Fatalf("config.toml = %q", got)
	}
}

func TestUpInGhModeRefusesWithoutAGhLogin(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), "[github]\nmode = \"gh\"\n")
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("needs the gh CLI on the host")
	h.gh = fakeGh(t, 1)
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("`gh auth token` fails")
	if h.anyCall("env create") {
		t.Error("created a sandbox without a gh login")
	}
}

func fakeGh(t *testing.T, exitCode int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gh")
	testutil.WriteFile(t, path, fmt.Sprintf("#!/bin/sh\necho gho_fake\nexit %d\n", exitCode))
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUpRefusesWhileSbxHasAGlobalGitHubSecret(t *testing.T) {
	h := setup(t)
	h.sbx.SetSecrets(`{"secrets": [{"scope": "global", "type": "service", "name": "github", "secret": "(stored)"}]}`)
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("sbx has a global github secret")
	h.expectStderr("sbx secret rm github -f")
	if h.anyCall("env create") {
		t.Error("created a sandbox that could fall back to the global github secret")
	}
}

func TestUpFailsClosedWhenItCannotReadSbxSecrets(t *testing.T) {
	h := setup(t)
	h.sbx.SetSecrets(`[{"scope": "global", "type": "service", "service": "github"}]`)
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("cannot read sbx secret ls --json")
	h.sbx.SetSecrets(`{"items": []}`)
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("expected a secrets list")
}
