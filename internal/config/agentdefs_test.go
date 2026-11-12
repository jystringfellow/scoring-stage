package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

const exampleAgent = `
[agent.example]
kit = "registry.example.com/sbx/example-mixin:1.2.3"
login_files = ["/home/agent/.example/token", { path = "/home/agent/.example/onboarding.json", optional = true }]

[agent.example.bindings.example]
apiKey = { domains = ["api.example.com"] }

[agent.example.secrets.example]
command = "{gh} auth token"
`

func homeWith(t *testing.T, config string) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), config)
	return home
}

func TestAUserConfigDefinesAnAgentWithItsKitBindingsSecretAndLoginFiles(t *testing.T) {
	home := homeWith(t, "agents = [\"example\"]\ncopy_logins = [\"example\"]\n"+exampleAgent)
	settings := load(t, home)
	composition := composeWith(t, settings, Selection{Gh: "/usr/bin/gh"})
	if !reflect.DeepEqual(composition.Agents, []string{"example"}) {
		t.Errorf("agents = %v", composition.Agents)
	}
	if got := sources(composition.Kits); got[0] != "registry.example.com/sbx/example-mixin:1.2.3" {
		t.Errorf("kits = %v", got)
	}
	if got := composition.Bindings["example"]; !reflect.DeepEqual(got, domains("apiKey", "api.example.com")) {
		t.Errorf("binding = %v", got)
	}
	if got := composition.Secrets["example"]; !reflect.DeepEqual(got, map[string]any{"command": "/usr/bin/gh auth token"}) {
		t.Errorf("secret = %v", got)
	}
	want := []LoginFile{{Path: "/home/agent/.example/token", Required: true}, {Path: "/home/agent/.example/onboarding.json"}}
	if got := settings.Agents.LoginFiles("example"); !reflect.DeepEqual(got, want) {
		t.Errorf("login files = %v", got)
	}
	if !settings.CopiesLogin("example") {
		t.Error("copy_logins refused a defined agent with login files")
	}
	if got := settings.SavedLogin("example", want[0]); got != filepath.Join(home, "logins", "example", "token") {
		t.Errorf("saved login = %s", got)
	}
	if got := settings.Agents.Names(); !reflect.DeepEqual(got, []string{"agy", "claude", "codex", "copilot", "example"}) {
		t.Errorf("names = %v", got)
	}
	if got := settings.StageBindings()["example"]; got == nil || !reflect.DeepEqual(got.Reasons, []string{"agent example"}) {
		t.Errorf("stage bindings = %v", got)
	}
}

func TestADefinedAgentIsNamedByARepositoryABookAndTheFlagLikeABuiltInOne(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), `books = ["`+book(t, dir, "org", "agents = [\"example\"]\n")+`"]`+"\n"+exampleAgent)
	repoDir := filepath.Join(dir, "repo")
	testutil.WriteFile(t, filepath.Join(repoDir, RepoConfigPath), "agents = [\"example\"]\n")
	repo, err := LoadRepoConfig(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	for name, selection := range map[string]Selection{
		"repository": {Repo: repo},
		"book":       {Books: []string{"org"}},
		"flag":       {Agents: []string{"example"}},
		"reference":  {Agents: []string{"registry.example.com/sbx/example-mixin:9.9.9"}},
	} {
		composition := composeWith(t, load(t, home), selection)
		if !reflect.DeepEqual(composition.Agents, []string{"example"}) || composition.Bindings["example"] == nil {
			t.Errorf("%s: agents %v bindings %v", name, composition.Agents, composition.Bindings)
		}
	}
	_, err = Compose(load(t, home), Selection{Agents: []string{"exampel"}})
	expectError(t, err, "unknown agent exampel (known: agy, claude, codex, copilot, example")
}

func TestADefinitionOfABuiltInNameOverridesItAndKeepsWhatItDoesNotSet(t *testing.T) {
	home := homeWith(t, "[agent.claude]\nkit = \"docker.io/sbx/claude-mixin:9.9.9\"\n")
	composition := composeWith(t, load(t, home), Selection{Agents: []string{"claude"}})
	if got := sources(composition.Kits)[0]; got != "docker.io/sbx/claude-mixin:9.9.9" {
		t.Errorf("kit = %s", got)
	}
	if got := composition.Bindings["anthropic"]; !reflect.DeepEqual(got, builtinAgents["claude"].bindings["anthropic"]) {
		t.Errorf("binding = %v", got)
	}

	home = homeWith(t, "[agent.copilot]\nkit = \"docker.io/sbx/copilot-mixin:9.9.9\"\nsecrets = {}\n[agent.copilot.bindings.copilot]\napiKey = { domains = [\"api.githubcopilot.com\"] }\n")
	composition = composeWith(t, load(t, home), Selection{Agents: []string{"copilot"}, Gh: "/usr/bin/gh"})
	if got := composition.Bindings["copilot"]; !reflect.DeepEqual(got, domains("apiKey", "api.githubcopilot.com")) {
		t.Errorf("binding = %v", got)
	}
	if _, set := composition.Secrets["copilot"]; set {
		t.Error("an emptied secrets table kept the built-in secret")
	}
}

func TestAnAgentsDefaultSecretThatNeedsGhIsLeftOutWithoutGhAndYieldsToTheUsersSecret(t *testing.T) {
	home := homeWith(t, exampleAgent)
	composition := composeWith(t, load(t, home), Selection{Agents: []string{"example", "copilot"}})
	for _, service := range []string{"example", "copilot"} {
		if _, set := composition.Secrets[service]; set {
			t.Errorf("%s secret without gh", service)
		}
	}
	home = homeWith(t, exampleAgent+"[secrets.example]\ncommand = \"cat /tmp/key\"\n")
	composition = composeWith(t, load(t, home), Selection{Agents: []string{"example"}, Gh: "/usr/bin/gh"})
	if got := composition.Secrets["example"]; !reflect.DeepEqual(got, map[string]any{"command": "cat /tmp/key"}) {
		t.Errorf("secret = %v", got)
	}
}

func TestARepositoryCannotDefineAnAgent(t *testing.T) {
	repoDir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(repoDir, RepoConfigPath), "agents = [\"example\"]\n"+exampleAgent)
	_, err := LoadRepoConfig(repoDir)
	expectError(t, err, "a repository cannot define agents")
}

func TestABookCannotDefineAnAgent(t *testing.T) {
	_, err := LoadBook(BuiltinAgents(), book(t, t.TempDir(), "org", exampleAgent))
	expectError(t, err, "books cannot define agents")
	_, err = LoadBook(BuiltinAgents(), book(t, t.TempDir(), "org", "[[repos]]\nmatch = \"api\"\n[repos.agent.example]\nkit = \"docker.io/org/kit:1\"\n"))
	expectError(t, err, "unknown keys agent")
}

func TestABookCannotClaimADefinedAgentsServicesOrHosts(t *testing.T) {
	dir := t.TempDir()
	for body, fragment := range map[string]string{
		"[secrets.example]\ncommand = \"cat /tmp/k\"\n":                                                           "secret example is reserved",
		"[secrets.feed]\ncommand = \"cat /tmp/k\"\n[bindings.feed]\napiKey = { domains = [\"*.example.com\"] }\n": "a host a reserved service uses",
	} {
		home := filepath.Join(t.TempDir(), "home")
		testutil.WriteFile(t, filepath.Join(home, "config.toml"), `books = ["`+book(t, filepath.Join(dir, fragment), "org", body)+`"]`+"\n"+exampleAgent)
		_, err := LoadSettings(home)
		expectError(t, err, fragment)
		_, err = Compose(load(t, homeWith(t, exampleAgent)), Selection{Books: []string{filepath.Join(dir, fragment, "org")}})
		expectError(t, err, fragment)
	}
}

func TestAgentDefinitionsAreDecodedStrictly(t *testing.T) {
	kit := "kit = \"docker.io/org/kit:1\"\n"
	for body, fragment := range map[string]string{
		"agent = [\"example\"]\n":                                                                                          "agent must be a table",
		"[agent.Example]\n" + kit:                                                                                          "agent name",
		"[agent.\"a/b\"]\n" + kit:                                                                                          "agent name",
		"[agent.\"ex​ample\"]\n" + kit:                                                                                     "agent name",
		"[agent.example]\n":                                                                                                "kit is required",
		"[agent.example]\nkit = \"claude\"\n":                                                                              "kit must be a kit reference",
		"[agent.example]\nkit = \"docker.io/org/kit:1​\"\n":                                                                "kit must be a kit reference",
		"[agent.example]\nkit = [\"docker.io/org/kit:1\"]\n":                                                               "kit must be a kit reference",
		"[agent.example]\n" + kit + "mixin = \"x\"\n":                                                                      "unknown keys mixin",
		"[agent.example]\n" + kit + "bindings = \"x\"\n":                                                                   "bindings must be a table",
		"[agent.example]\n" + kit + "secrets = \"x\"\n":                                                                    "secrets must be a table",
		"[agent.example]\n" + kit + "secrets = { example = \"x\" }\n":                                                      "a secret is a table",
		"[agent.example]\n" + kit + "[agent.example.secrets.example]\ncommand = [\"cat\"]\n":                               "command must be a string",
		"[agent.example]\n" + kit + "[agent.example.secrets.github]\ncommand = \"cat /tmp/k\"\n":                           "come only from [github] mode",
		"[agent.example]\n" + kit + "[agent.example.secrets.stage-broker]\ncommand = \"cat /tmp/k\"\n":                     "stage-broker is stage's own",
		"[agent.example]\n" + kit + "[agent.example.secrets.\"exa​mple\"]\ncommand = \"cat /tmp/k\"\n":                     "contains hidden characters",
		"[agent.example]\n" + kit + "[agent.example.bindings.\"exa​mple\"]\napiKey = { domains = [\"x.example.com\"] }\n":  "contains hidden characters",
		"[agent.example]\n" + kit + "[agent.example.bindings.example]\napiKey = { domains = [\"x.example​.com\"] }\n":      "contains hidden characters",
		"[agent.example]\n" + kit + "[agent.example.bindings.example]\napiKey = { domains = [\"api.github.com\"] }\n":      "GitHub credentials come only from [github] mode",
		"[agent.example]\n" + kit + "[agent.example.bindings.example]\napiKey = { domains = [\"api.exampl?.com\"] }\n":     "glob characters other than *",
		"[agent.example]\n" + kit + "[agent.example.bindings.example]\napiKey = { hosts = [\"x.example.com\"] }\n":         "must be { domains = [...] }",
		"[agent.example]\n" + kit + "[agent.example.bindings.stage-broker]\napiKey = { domains = [\"localhost:9911\"] }\n": "stage-broker is stage's own",
		"[agent.example]\n" + kit + "login_files = \"/home/agent/.x\"\n":                                                   "login_files must be a list",
		"[agent.example]\n" + kit + "login_files = [\"home/agent/.x\"]\n":                                                  "must be an absolute path in the sandbox",
		"[agent.example]\n" + kit + "login_files = [\"~/.x/token\"]\n":                                                     "must be an absolute path in the sandbox",
		"[agent.example]\n" + kit + "login_files = [\"/home/agent/../../etc/x\"]\n":                                        "must be an absolute path in the sandbox",
		"[agent.example]\n" + kit + "login_files = [\"/home/agent/.x/\"]\n":                                                "must be an absolute path in the sandbox",
		"[agent.example]\n" + kit + "login_files = [\"/home/agent/.x/to​ken\"]\n":                                          "must be an absolute path in the sandbox",
		"[agent.example]\n" + kit + "login_files = [{ path = \"/home/agent/.x\", required = true }]\n":                     "a login file is a path or { path, optional }",
		"[agent.example]\n" + kit + "login_files = [{ path = \"/home/agent/.x\", optional = \"yes\" }]\n":                  "a login file is a path or { path, optional }",
		"[agent.example]\n" + kit + "login_files = [\"/home/agent/.a/token\", \"/home/agent/.b/token\"]\n":                 "are both saved as token",
		"[agent.example]\nkit = \"docker.io/sbx/claude-mixin:9.9.9\"\n":                                                    "agents claude and example both use docker.io/sbx/claude-mixin",
	} {
		_, err := LoadSettings(homeWith(t, body))
		if err == nil || !strings.Contains(err.Error(), fragment) {
			t.Errorf("%q: got %v, want %q", body, err, fragment)
		}
	}
}

func TestADefinedAgentsKitMayBeALocalDirectoryMatchedExactly(t *testing.T) {
	home := homeWith(t, "[agent.example]\nkit = \"./kits/example\"\n[agent.example.bindings.example]\napiKey = { domains = [\"api.example.com\"] }\n")
	composition := composeWith(t, load(t, home), Selection{Agents: []string{"example"}})
	if got := sources(composition.Kits)[0]; got != filepath.Join(Resolve(home), "kits", "example") {
		t.Errorf("kit = %s", got)
	}
	if !reflect.DeepEqual(composition.Agents, []string{"example"}) || composition.Bindings["example"] == nil {
		t.Errorf("agents %v bindings %v", composition.Agents, composition.Bindings)
	}
}

func TestTwoChosenAgentsCannotSetOneServiceDifferently(t *testing.T) {
	home := homeWith(t, "[agent.example]\nkit = \"docker.io/org/kit:1\"\n[agent.example.bindings.openai]\napiKey = { domains = [\"api.openai.com\"] }\n")
	settings := load(t, home)
	composeWith(t, settings, Selection{Agents: []string{"example"}})
	_, err := Compose(settings, Selection{Agents: []string{"codex", "example"}})
	expectError(t, err, "agent example sets binding openai differently from agent codex")
}

func TestAgentPolicyRefusesADefinitionThatChangesWhichKitANameMeans(t *testing.T) {
	managedFiles(t, "allowed_agents = [\"claude\", \"example\"]\n")
	_, err := LoadSettings(homeWith(t, exampleAgent))
	expectError(t, err, "agent example is defined in")
	_, err = LoadSettings(homeWith(t, "[agent.claude]\nkit = \"registry.example.com/other/kit:1\"\n"))
	expectError(t, err, "agent claude is defined in")
	settings := load(t, homeWith(t, "[agent.claude]\nkit = \"docker.io/sbx/claude-mixin:9.9.9\"\n"))
	composeWith(t, settings, Selection{Agents: []string{"claude"}})
}

func TestManagedCopyLoginsRefusesLoginFilesFromADefinition(t *testing.T) {
	managedFiles(t, "copy_logins = [\"agy\", \"example\"]\n")
	_, err := LoadSettings(homeWith(t, exampleAgent))
	expectError(t, err, "agent example sets login_files")
	_, err = LoadSettings(homeWith(t, "[agent.agy]\nkit = \"docker.io/sbx/antigravity-mixin:1.0.0\"\nlogin_files = [\"/home/agent/.ssh/id_ed25519\"]\n"))
	expectError(t, err, "agent agy sets login_files")
	load(t, homeWith(t, "copy_logins = [\"agy\"]\n[agent.agy]\nkit = \"docker.io/sbx/antigravity-mixin:9.9.9\"\n"))
}

func TestSaveAgentsAndTheListedNamesIncludeDefinedAgents(t *testing.T) {
	home := homeWith(t, exampleAgent)
	if got := AgentNames(home); !reflect.DeepEqual(got, []string{"agy", "claude", "codex", "copilot", "example"}) {
		t.Errorf("names = %v", got)
	}
	expectError(t, SaveAgents(home, []string{"exampel"}), "unknown agent exampel (known: agy, claude, codex, copilot, example)")
	if err := SaveAgents(home, []string{"example"}); err != nil {
		t.Fatal(err)
	}
	if got := load(t, home).AgentEntries(); !reflect.DeepEqual(got, []string{"example"}) {
		t.Errorf("agents = %v", got)
	}
	if got := AgentNames(homeWith(t, "[agent.example]\n")); !reflect.DeepEqual(got, []string{"agy", "claude", "codex", "copilot"}) {
		t.Errorf("names with an unreadable definition = %v", got)
	}
}

func TestNoBuiltInAgentsHostCountsAsGitHubs(t *testing.T) {
	for _, spec := range builtinAgents {
		for _, binding := range spec.bindings {
			hosts, err := bindingDomains(binding)
			if err != nil {
				t.Fatal(err)
			}
			for _, host := range hosts {
				if ReachesGitHub(host) {
					t.Errorf("%s was treated as reaching GitHub", host)
				}
			}
		}
	}
}
