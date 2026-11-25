package scoringstage

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflowStep struct {
	Name string            `yaml:"name"`
	ID   string            `yaml:"id"`
	If   string            `yaml:"if"`
	Uses string            `yaml:"uses"`
	With map[string]string `yaml:"with"`
	Env  map[string]string `yaml:"env"`
	Run  string            `yaml:"run"`
}

type workflowJob struct {
	Needs       any            `yaml:"needs"`
	Permissions map[string]any `yaml:"permissions"`
	Steps       []workflowStep `yaml:"steps"`
}

func releaseWorkflow(t *testing.T) (string, map[string]workflowJob) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]workflowJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(content, &workflow); err != nil {
		t.Fatal(err)
	}
	return string(content), workflow.Jobs
}

func homebrewStep(t *testing.T, job workflowJob, match func(workflowStep) bool) workflowStep {
	t.Helper()
	for _, step := range job.Steps {
		if match(step) {
			return step
		}
	}
	t.Fatalf("the homebrew job has no such step: %+v", job.Steps)
	return workflowStep{}
}

const appTokenAction = "actions/create-github-app-token@64ab729a6294000f5aa4a4bc0b73d1f32bc45510"

func TestTheHomebrewJobPushesWithATokenMintedFromThePublisherAppForTheTapAlone(t *testing.T) {
	text, jobs := releaseWorkflow(t)
	job := jobs["homebrew"]
	if len(job.Permissions) != 0 {
		t.Errorf("the homebrew job has permissions on this repository: %v", job.Permissions)
	}
	if strings.Contains(text, "HOMEBREW_TAP_TOKEN") {
		t.Error("the workflow still names HOMEBREW_TAP_TOKEN")
	}
	mint := homebrewStep(t, job, func(s workflowStep) bool { return strings.HasPrefix(s.Uses, "actions/create-github-app-token@") })
	if mint.Uses != appTokenAction {
		t.Errorf("uses %q, want it pinned as %q", mint.Uses, appTokenAction)
	}
	want := map[string]string{
		"client-id":           "${{ vars.PUBLISHER_APP_CLIENT_ID }}",
		"private-key":         "${{ secrets.PUBLISHER_APP_PRIVATE_KEY }}",
		"owner":               "${{ github.repository_owner }}",
		"repositories":        "homebrew-tap",
		"permission-contents": "write",
	}
	if !maps.Equal(mint.With, want) {
		t.Errorf("the token is minted with %v, want exactly %v", mint.With, want)
	}
	publish := homebrewStep(t, job, func(s workflowStep) bool { return strings.Contains(s.Run, "git push") })
	if publish.Env["TAP_TOKEN"] != "${{ steps."+mint.ID+".outputs.token }}" {
		t.Errorf("the push step's TAP_TOKEN is %q, want the minted token", publish.Env["TAP_TOKEN"])
	}
	if !strings.Contains(publish.Env["GIT_CONFIG_VALUE_0"], `password=$TAP_TOKEN`) || publish.Env["GIT_CONFIG_KEY_0"] != "credential.https://github.com.helper" {
		t.Errorf("the token does not reach git through a credential helper: %v", publish.Env)
	}
	for _, step := range job.Steps {
		if strings.Contains(step.Run, "${{") {
			t.Errorf("step %q puts an expression in its script; pass it in env instead:\n%s", step.Name, step.Run)
		}
	}
}

func TestTheHomebrewJobCommitsToTheTapAsThePublisherAppsBot(t *testing.T) {
	_, jobs := releaseWorkflow(t)
	job := jobs["homebrew"]
	mint := homebrewStep(t, job, func(s workflowStep) bool { return s.Uses == appTokenAction })
	publish := homebrewStep(t, job, func(s workflowStep) bool { return strings.Contains(s.Run, "git push") })
	if publish.Env["APP_SLUG"] != "${{ steps."+mint.ID+".outputs.app-slug }}" {
		t.Errorf("APP_SLUG is %q, want the minted token's app slug", publish.Env["APP_SLUG"])
	}
	for _, want := range []string{`bot="${APP_SLUG}[bot]"`, `gh api "/users/${bot}" --jq .id`, `user.name="$bot"`, `user.email="${id}+${bot}@users.noreply.github.com"`} {
		if !strings.Contains(publish.Run, want) {
			t.Errorf("the push step lacks %s:\n%s", want, publish.Run)
		}
	}
	if strings.Contains(publish.Run, "github-actions[bot]") {
		t.Error("the tap commit is still authored as github-actions[bot]")
	}
}

func TestTheHomebrewJobSkipsAndSucceedsWithoutThePublisherApp(t *testing.T) {
	_, jobs := releaseWorkflow(t)
	job := jobs["homebrew"]
	check := homebrewStep(t, job, func(s workflowStep) bool { return s.ID == "credentials" })
	if check.Env["PUBLISHER_APP_CLIENT_ID"] != "${{ vars.PUBLISHER_APP_CLIENT_ID }}" || check.Env["PUBLISHER_APP_PRIVATE_KEY"] != "${{ secrets.PUBLISHER_APP_PRIVATE_KEY }}" {
		t.Errorf("the check reads %v", check.Env)
	}
	for _, step := range job.Steps[slices.IndexFunc(job.Steps, func(s workflowStep) bool { return s.ID == "credentials" })+1:] {
		if step.If != "steps.credentials.outputs.enabled == 'true'" {
			t.Errorf("step %q runs without the publisher app: if = %q", step.Name+step.Uses, step.If)
		}
	}
	for _, env := range []map[string]string{
		{"PUBLISHER_APP_CLIENT_ID": "", "PUBLISHER_APP_PRIVATE_KEY": ""},
		{"PUBLISHER_APP_CLIENT_ID": "Iv23example", "PUBLISHER_APP_PRIVATE_KEY": ""},
		{"PUBLISHER_APP_CLIENT_ID": "", "PUBLISHER_APP_PRIVATE_KEY": "key"},
		{"PUBLISHER_APP_CLIENT_ID": "Iv23example", "PUBLISHER_APP_PRIVATE_KEY": "key"},
	} {
		output := filepath.Join(t.TempDir(), "output")
		command := exec.Command("bash", "-c", check.Run)
		command.Env = []string{"PATH=" + os.Getenv("PATH"), "GITHUB_OUTPUT=" + output}
		for key, value := range env {
			command.Env = append(command.Env, key+"="+value)
		}
		said, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("the check failed with %v: %s", env, said)
		}
		recorded, _ := os.ReadFile(output)
		enabled := env["PUBLISHER_APP_CLIENT_ID"] != "" && env["PUBLISHER_APP_PRIVATE_KEY"] != ""
		if got := strings.TrimSpace(string(recorded)); got != "enabled="+map[bool]string{true: "true", false: "false"}[enabled] {
			t.Errorf("with %v the check recorded %q", env, got)
		}
		if !enabled && !strings.Contains(string(said), "skipping the Homebrew tap") {
			t.Errorf("with %v the check said %q", env, said)
		}
	}
}

var pinnedAction = regexp.MustCompile(`^[\w.-]+/[\w./-]+@[0-9a-f]{40}$`)

func TestEveryReleaseActionIsPinnedByCommit(t *testing.T) {
	_, jobs := releaseWorkflow(t)
	for name, job := range jobs {
		for _, step := range job.Steps {
			if step.Uses != "" && !pinnedAction.MatchString(step.Uses) {
				t.Errorf("job %s uses %q, not pinned by a commit sha", name, step.Uses)
			}
		}
	}
}
