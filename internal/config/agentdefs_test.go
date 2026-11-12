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
		"agent = [\"example\"]\n":                                                                           "agent must be a table",
		"[agent.Example]\n" + kit:                                                                           "agent name",
		"[agent.\"a/b\"]\n" + kit:                                                                           "agent name",
		"[agent.\"ex\u200bample\"]\n" + kit:                                                                 "agent name",
		"[agent.example]\n":                                                                                 "kit is required",
		"[agent.example]\nkit = \"claude\"\n":                                                               "kit must be a kit reference",
		"[agent.example]\nkit = \"docker.io/org/kit:1\u200b\"\n":                                            "kit must be a kit reference",
		"[agent.example]\nkit = [\"docker.io/org/kit:1\"]\n":                                                "kit must be a kit reference",
		"[agent.example]\n" + kit + "mixin = \"x\"\n":                                                       "unknown keys mixin",
		"[agent.example]\n" + kit + "bindings = \"x\"\n":                                                    "bindings must be a table",
		"[agent.example]\n" + kit + "secrets = \"x\"\n":                                                     "secrets must be a table",
		"[agent.example]\n" + kit + "secrets = { example = \"x\" }\n":                                       "a secret is a table",
		"[agent.example]\n" + kit + "[agent.example.secrets.example]\ncommand = [\"cat\"]\n":                "command must be a string",
		"[agent.example]\n" + kit + "[agent.example.secrets.github]\ncommand = \"cat /tmp/k\"\n":            "come only from [github] mode",
		"[agent.example]\n" + kit + "[agent.example.secrets.stage-broker]\ncommand = \"cat /tmp/k\"\n":      "stage-broker is stage's own",
		"[agent.example]\n" + kit + "[agent.example.secrets.\"exa\u200bmple\"]\ncommand = \"cat /tmp/k\"\n": "contains hidden characters",
		"[agent.example]\n" + kit + "[agent.example.bindings.\"exa\u200bmple\"]\napiKey = { domains = [\"x.example.com\"] }\n": "contains hidden characters",
		"[agent.example]\n" + kit + "[agent.example.bindings.example]\napiKey = { domains = [\"x.example\u200b.com\"] }\n":     "contains hidden characters",
		"[agent.example]\n" + kit + "[agent.example.bindings.example]\napiKey = { domains = [\"api.github.com\"] }\n":          "GitHub credentials come only from [github] mode",
		"[agent.example]\n" + kit + "[agent.example.bindings.example]\napiKey = { domains = [\"api.exampl?.com\"] }\n":         "glob characters other than *",
		"[agent.example]\n" + kit + "[agent.example.bindings.example]\napiKey = { hosts = [\"x.example.com\"] }\n":             "must be { domains = [...] }",
		"[agent.example]\n" + kit + "[agent.example.bindings.stage-broker]\napiKey = { domains = [\"localhost:9911\"] }\n":     "stage-broker is stage's own",
		"[agent.example]\n" + kit + "login_files = \"/home/agent/.x\"\n":                                                       "login_files must be a list",
		"[agent.example]\n" + kit + "login_files = [\"home/agent/.x\"]\n":                                                      "must be an absolute path in the sandbox",
		"[agent.example]\n" + kit + "login_files = [\"~/.x/token\"]\n":                                                         "must be an absolute path in the sandbox",
		"[agent.example]\n" + kit + "login_files = [\"/home/agent/../../etc/x\"]\n":                                            "must be an absolute path in the sandbox",
		"[agent.example]\n" + kit + "login_files = [\"/home/agent/.x/\"]\n":                                                    "must be an absolute path in the sandbox",
		"[agent.example]\n" + kit + "login_files = [\"/home/agent/.x/to\u200bken\"]\n":                                         "must be an absolute path in the sandbox",
		"[agent.example]\n" + kit + "login_files = [{ path = \"/home/agent/.x\", required = true }]\n":                         "a login file is a path or { path, optional }",
		"[agent.example]\n" + kit + "login_files = [{ path = \"/home/agent/.x\", optional = \"yes\" }]\n":                      "a login file is a path or { path, optional }",
		"[agent.example]\n" + kit + "login_files = [\"/home/agent/.a/token\", \"/home/agent/.b/token\"]\n":                     "are both saved as token",
		"[agent.example]\n" + kit + "[agent.example.secrets.example]\ncomand = \"cat /tmp/k\"\n":                               "unknown keys comand",
		"[agent.example]\n" + kit + "[agent.example.secrets.example]\nrefresh = \"1h\"\n":                                      "command is required",
		"[agent.example]\n" + kit + "[agent.example.secrets.GitHub]\ncommand = \"cat /tmp/k\"\n":                               "come only from [github] mode",
		"[agent.example]\n" + kit + "[agent.example.secrets.\" github\"]\ncommand = \"cat /tmp/k\"\n":                          "come only from [github] mode",
		"[agent.example]\n" + kit + "[agent.example.bindings.GITHUB]\napiKey = { domains = [\"x.example.com\"] }\n":            "come only from [github] mode",
		"[secrets.GitHub]\ncommand = \"cat /tmp/k\"\n":                                                                         "come only from [github] mode",
		"[bindings.\"GITHUB \"]\napiKey = { domains = [\"x.example.com\"] }\n":                                                 "come only from [github] mode",
		"[agent.example]\n" + kit + "[agent.example.secrets.example]\ncommand = \"echo '{gh}'\"\n":                             "{gh} must stand unquoted",
		"[agent.example]\n" + kit + "[agent.example.secrets.example]\ncommand = 'sh -c \"{gh} auth token\"'\n":                 "{gh} must stand unquoted",
		"[agent.example]\n" + kit + "[agent.example.secrets.example]\ncommand = 'echo \\{gh}'\n":                               "{gh} must stand unquoted",
		"[agent.example]\n" + kit + "[agent.example.secrets.example]\ncommand = \"x{gh} auth token\"\n":                        "{gh} must stand unquoted",
		"[agent.example]\n" + kit + "login_files = [\"/home/agent/.a/Token\", \"/home/agent/.b/token\"]\n":                     "are both saved as",
		"[agent.example]\nkit = \"docker.io/sbx/claude-mixin:9.9.9\"\n":                                                        "agents claude and example both use docker.io/sbx/claude-mixin",
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

func TestUnderAnAgentPolicyADefinitionMayOnlyPinAnotherVersionOfABuiltInAgentsKit(t *testing.T) {
	pinned := "[agent.claude]\nkit = \"docker.io/sbx/claude-mixin:9.9.9\"\n"
	for _, policy := range []string{"allowed_agents = [\"claude\", \"example\"]\n", "denied_agents = [\"codex\"]\n"} {
		managedFiles(t, policy)
		for _, body := range []string{
			exampleAgent,
			"[agent.claude]\nkit = \"registry.example.com/other/kit:1\"\n",
			pinned + "[agent.claude.bindings.extra]\napiKey = { domains = [\"api.example.com\"] }\n",
			pinned + "[agent.claude.secrets.anthropic]\ncommand = \"cat /tmp/k\"\n",
			pinned + "login_files = [\"/home/agent/.claude/token\"]\n",
			pinned + "bindings = {}\n",
		} {
			_, err := LoadSettings(homeWith(t, body))
			if err == nil || !strings.Contains(err.Error(), "may only pin another version of a built-in agent's kit") {
				t.Errorf("%s%q: got %v", policy, body, err)
			}
		}
		composeWith(t, load(t, homeWith(t, pinned)), Selection{Agents: []string{"claude"}})
	}
}

func TestManagedCopyLoginsRefusesADefinitionThatChoosesTheFilesOrMovesAListedNameToAnotherKit(t *testing.T) {
	managedFiles(t, "copy_logins = [\"agy\", \"example\"]\n")
	_, err := LoadSettings(homeWith(t, "[agent.claude]\nkit = \"docker.io/sbx/claude-mixin:9.9.9\"\nlogin_files = [\"/home/agent/.claude/token\"]\n"))
	expectError(t, err, "agent claude sets login_files")
	_, err = LoadSettings(homeWith(t, "[agent.agy]\nkit = \"docker.io/sbx/antigravity-mixin:1.0.0\"\nlogin_files = [\"/home/agent/.ssh/id_ed25519\"]\n"))
	expectError(t, err, "agent agy sets login_files")
	_, err = LoadSettings(homeWith(t, "[agent.agy]\nkit = \"registry.example.com/other/kit:1\"\n"))
	expectError(t, err, "agent agy is defined on a kit that is not its built-in one")
	_, err = LoadSettings(homeWith(t, "[agent.example]\nkit = \"registry.example.com/other/kit:1\"\n"))
	expectError(t, err, "agent example is defined on a kit that is not its built-in one")
	load(t, homeWith(t, "copy_logins = [\"agy\"]\n[agent.agy]\nkit = \"docker.io/sbx/antigravity-mixin:9.9.9\"\n"))
	load(t, homeWith(t, "[agent.claude]\nkit = \"registry.example.com/other/kit:1\"\n"))
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

func TestOnlyTheListedHostsBesideGitHubAreExemptAndABuiltInAgentBindsEachOne(t *testing.T) {
	bound := map[string]bool{}
	for _, spec := range builtinAgents {
		for _, binding := range spec.bindings {
			hosts, _ := bindingDomains(binding)
			for _, host := range hosts {
				bound[normalizeHost(host)] = true
			}
		}
	}
	for _, host := range hostsBesideGitHub {
		if !bound[host] {
			t.Errorf("%s is exempt from the GitHub rule but no built-in agent binds it", host)
		}
		for _, wider := range []string{"x." + host, "*." + host, strings.SplitN(host, ".", 2)[1]} {
			if !ReachesGitHub(wider) {
				t.Errorf("%s was exempted along with %s", wider, host)
			}
		}
	}
	for _, host := range []string{"api.github.com", "github.com", "uploads.github.com", "*.github.com", "raw.githubusercontent.com", "models.github.ai"} {
		if !ReachesGitHub(host) {
			t.Errorf("%s was not treated as reaching GitHub", host)
		}
		_, err := LoadSettings(homeWith(t, "[agent.claude]\nkit = \"docker.io/sbx/claude-mixin:9.9.9\"\n[agent.claude.bindings.anthropic]\noauth = { domains = [\"api.anthropic.com\", \""+host+"\"] }\n"))
		expectError(t, err, "GitHub credentials come only from [github] mode")
	}
}

func TestGhMayStandNextToAShellSeparatorButNotNextToOtherText(t *testing.T) {
	definition := func(command string) string {
		return "[agent.example]\nkit = \"docker.io/org/kit:1\"\n[agent.example.secrets.example]\ncommand = '" + command + "'\n"
	}
	for command, want := range map[string]string{
		"true;{gh} auth token":        "true;/usr/bin/gh auth token",
		"echo $({gh} auth token)":     "echo $(/usr/bin/gh auth token)",
		"({gh} auth token)":           "(/usr/bin/gh auth token)",
		"{gh};true":                   "/usr/bin/gh;true",
		"true && {gh} auth token|cat": "true && /usr/bin/gh auth token|cat",
	} {
		composition := composeWith(t, load(t, homeWith(t, definition(command))), Selection{Agents: []string{"example"}, Gh: "/usr/bin/gh"})
		if got := composition.Secrets["example"]; !reflect.DeepEqual(got, map[string]any{"command": want}) {
			t.Errorf("%s: secret = %v", command, got)
		}
	}
	for _, command := range []string{"{gh}x auth token", "x{gh} auth token", "{gh}{gh}", "a={gh} auth token", "{gh}/../other"} {
		_, err := LoadSettings(homeWith(t, definition(command)))
		if err == nil || !strings.Contains(err.Error(), "{gh} must stand unquoted") {
			t.Errorf("%s: got %v", command, err)
		}
	}
}

func TestAPublishedKitReferenceIsWrittenInLowerCaseBeforeItsTag(t *testing.T) {
	const fragment = "has upper-case letters"
	for _, reference := range []string{"SBX/claude-mixin:1", "Docker.io/sbx/claude-mixin:1", "docker.io/SBX/claude-mixin:1", "docker.io/sbx/Claude-mixin:1", "Registry.example.com/org/kit@sha256:abc"} {
		for name, body := range map[string]string{
			"definition": "[agent.claude]\nkit = \"" + reference + "\"\n",
			"agents":     "agents = [\"" + reference + "\"]\n",
			"kits":       "kits = [{ source = \"" + reference + "\" }]\n",
			"base":       "base = \"" + reference + "\"\n",
		} {
			settings, err := LoadSettings(homeWith(t, body))
			if err == nil {
				_, err = Compose(settings, Selection{})
			}
			if err == nil || !strings.Contains(err.Error(), fragment) {
				t.Errorf("%s %s: got %v", name, reference, err)
			}
		}
		_, err := Compose(load(t, homeWith(t, "")), Selection{Agents: []string{reference}})
		expectError(t, err, fragment)
		_, err = LoadBook(BuiltinAgents(), book(t, t.TempDir(), "org", "kits = [\""+reference+"\"]\n"))
		expectError(t, err, fragment)
	}
	composeWith(t, load(t, homeWith(t, "agents = [\"docker.io/sbx/claude-mixin:1.0-RC1\"]\nkits = [\"registry.example.com/org/kit:Latest\"]\n")), Selection{})
}

func TestManagedSettingsJudgeADefinitionsKitByTheRegistryAndPathARegistryWouldRead(t *testing.T) {
	for policy, agent := range map[string]string{"allowed_agents = [\"claude\", \"agy\"]\n": "claude", "copy_logins = [\"agy\"]\n": "agy"} {
		managedFiles(t, policy)
		mixin := strings.TrimPrefix(repositoryOf(builtinAgents[agent].kit), "docker.io/sbx/")
		for _, reference := range []string{"sbx/" + mixin + ":1", "docker.io/sbx/" + mixin + ":1", "index.docker.io/sbx/" + mixin + ":1", "registry-1.docker.io/sbx/" + mixin + "@sha256:abc", "docker.io/sbx/" + mixin + ":1.0-RC1"} {
			if _, err := LoadSettings(homeWith(t, "[agent."+agent+"]\nkit = \""+reference+"\"\n")); err != nil {
				t.Errorf("%s%s: %v", policy, reference, err)
			}
		}
		for reference, fragment := range map[string]string{
			"SBX/" + mixin + ":1":                       "has upper-case letters",
			"Docker.io/sbx/" + mixin + ":1":             "has upper-case letters",
			"docker.io/Sbx/" + mixin + ":1":             "has upper-case letters",
			"registry.example.com/sbx/" + mixin + ":1":  "agent " + agent + " is defined",
			"localhost/sbx/" + mixin + ":1":             "agent " + agent + " is defined",
			"localhost:5000/sbx/" + mixin + ":1":        "agent " + agent + " is defined",
			"docker.io.example.com/sbx/" + mixin + ":1": "agent " + agent + " is defined",
			"docker.io/library/sbx/" + mixin + ":1":     "agent " + agent + " is defined",
			"docker.io/sbx/" + mixin + "-x:1":           "agent " + agent + " is defined",
			mixin + ":1":                                "agent " + agent + " is defined",
		} {
			_, err := LoadSettings(homeWith(t, "[agent."+agent+"]\nkit = \""+reference+"\"\n"))
			if err == nil || !strings.Contains(err.Error(), fragment) {
				t.Errorf("%s%s: got %v, want %q", policy, reference, err, fragment)
			}
		}
	}
}

func TestServiceNamesAreWrittenInTheirCanonicalForm(t *testing.T) {
	kit := "[agent.example]\nkit = \"docker.io/org/kit:1\"\n"
	for body, fragment := range map[string]string{
		kit + "[agent.example.bindings.OpenAI]\napiKey = { domains = [\"x.example.com\"] }\n": `binding "OpenAI" must be written "openai"`,
		kit + "[agent.example.secrets.\" copilot\"]\ncommand = \"cat /tmp/k\"\n":              `secret " copilot" must be written "copilot"`,
		kit + "[agent.example.secrets.\" \"]\ncommand = \"cat /tmp/k\"\n":                     `secret " " has no name`,
		"[secrets.Copilot]\ncommand = \"cat /tmp/k\"\n":                                       `secret "Copilot" must be written "copilot"`,
		"[bindings.\"openai \"]\napiKey = { domains = [\"x.example.com\"] }\n":                `binding "openai " must be written "openai"`,
	} {
		_, err := LoadSettings(homeWith(t, body))
		if err == nil || !strings.Contains(err.Error(), fragment) {
			t.Errorf("%q: got %v, want %s", body, err, fragment)
		}
	}
}

func TestTwoAgentsThatDisagreeOnASecretAreRefusedWithOrWithoutGh(t *testing.T) {
	home := homeWith(t, "[agent.example]\nkit = \"docker.io/org/kit:1\"\n[agent.example.secrets.copilot]\ncommand = \"cat /tmp/k\"\n")
	for _, gh := range []string{"", "/usr/bin/gh"} {
		for _, chosen := range [][]string{{"copilot", "example"}, {"example", "copilot"}} {
			_, err := Compose(load(t, home), Selection{Agents: chosen, Gh: gh})
			expectError(t, err, "agent "+chosen[1]+" sets secret copilot differently from agent "+chosen[0])
		}
	}
	same := homeWith(t, "[agent.example]\nkit = \"docker.io/org/kit:1\"\n[agent.example.secrets.copilot]\ncommand = \"{gh} auth token\"\n")
	composition := composeWith(t, load(t, same), Selection{Agents: []string{"copilot", "example"}})
	if _, set := composition.Secrets["copilot"]; set || len(composition.Warnings) != 1 {
		t.Errorf("secrets %v warnings %v", composition.Secrets, composition.Warnings)
	}
}

func TestCopyLoginsNamesTheAgentsThatHaveNoLoginFilesFromTheTable(t *testing.T) {
	_, err := LoadSettings(homeWith(t, "copy_logins = [\"example\"]\n[agent.example]\nkit = \"docker.io/org/kit:1\"\n"))
	expectError(t, err, "copy_logins: example cannot be copied (copyable: agy); claude, codex, copilot, example have no login files")
}

func TestAnEmptyTableOrListClearsWhatTheBuiltInAgentHad(t *testing.T) {
	home := homeWith(t, "[agent.claude]\nkit = \"docker.io/sbx/claude-mixin:9.9.9\"\nbindings = {}\n[agent.agy]\nkit = \"docker.io/sbx/antigravity-mixin:9.9.9\"\nlogin_files = []\n")
	settings := load(t, home)
	composition := composeWith(t, settings, Selection{Agents: []string{"claude", "agy"}})
	if _, set := composition.Bindings["anthropic"]; set {
		t.Error("an emptied bindings table kept the built-in binding")
	}
	if got := settings.Agents.LoginFiles("agy"); len(got) != 0 {
		t.Errorf("an emptied login_files list kept %v", got)
	}
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "copy_logins = [\"agy\"]\n"+testutil.ReadFile(t, filepath.Join(home, "config.toml")))
	_, err := LoadSettings(home)
	expectError(t, err, "copy_logins: agy cannot be copied")
}

func TestTheUsersOwnBindingOrSecretSettlesTwoAgentsThatDisagree(t *testing.T) {
	definition := "[agent.example]\nkit = \"docker.io/org/kit:1\"\n[agent.example.bindings.openai]\napiKey = { domains = [\"api.openai.com\"] }\n" +
		"[agent.example.secrets.shared]\ncommand = \"cat /tmp/a\"\n[agent.other]\nkit = \"docker.io/org/other:1\"\n[agent.other.secrets.shared]\ncommand = \"cat /tmp/b\"\n"
	_, err := Compose(load(t, homeWith(t, definition)), Selection{Agents: []string{"example", "other"}})
	expectError(t, err, "agent other sets secret shared differently from agent example")
	home := homeWith(t, definition+"[bindings.openai]\napiKey = { domains = [\"chatgpt.com\"] }\n[secrets.shared]\ncommand = \"cat /tmp/mine\"\n")
	composition := composeWith(t, load(t, home), Selection{Agents: []string{"codex", "example", "other"}})
	if got := composition.Bindings["openai"]; !reflect.DeepEqual(got, domains("apiKey", "chatgpt.com")) {
		t.Errorf("binding = %v", got)
	}
	if got := composition.Secrets["shared"]; !reflect.DeepEqual(got, map[string]any{"command": "cat /tmp/mine"}) {
		t.Errorf("secret = %v", got)
	}
}

func TestALocalKitIsTheSameKitHoweverItsPathIsWritten(t *testing.T) {
	home := homeWith(t, "[agent.example]\nkit = \"/opt/kits/example\"\n[agent.example.bindings.example]\napiKey = { domains = [\"api.example.com\"] }\n")
	for _, reference := range []string{"/opt/kits/example", "/opt/kits/example/", "/opt/./kits/example", "/opt/kits//example"} {
		composition := composeWith(t, load(t, home), Selection{Agents: []string{reference}})
		if !reflect.DeepEqual(composition.Agents, []string{"example"}) || composition.Bindings["example"] == nil {
			t.Errorf("%s: agents %v bindings %v", reference, composition.Agents, composition.Bindings)
		}
	}
	_, err := LoadSettings(homeWith(t, "[agent.example]\nkit = \"/opt/kits/example\"\n[agent.other]\nkit = \"/opt/kits/example/\"\n"))
	expectError(t, err, "agents example and other both use /opt/kits/example")
}

func TestGhInAnAgentsSecretIsSubstitutedShellQuotedAndItsAbsenceIsReported(t *testing.T) {
	home := homeWith(t, exampleAgent)
	composition := composeWith(t, load(t, home), Selection{Agents: []string{"example"}, Gh: "/opt/my tools/gh"})
	if got := composition.Secrets["example"]; !reflect.DeepEqual(got, map[string]any{"command": "'/opt/my tools/gh' auth token"}) {
		t.Errorf("secret = %v", got)
	}
	if len(composition.Warnings) != 0 {
		t.Errorf("warnings = %v", composition.Warnings)
	}
	composition = composeWith(t, load(t, home), Selection{Agents: []string{"example"}})
	want := []string{"agent example's secret example needs the gh CLI, which was not found, so the sandbox has no example secret"}
	if !reflect.DeepEqual(composition.Warnings, want) {
		t.Errorf("warnings = %v", composition.Warnings)
	}
	composition = composeWith(t, load(t, homeWith(t, exampleAgent+"[secrets.example]\ncommand = \"cat /tmp/key\"\n")), Selection{Agents: []string{"example"}})
	if len(composition.Warnings) != 0 {
		t.Errorf("warned although the user supplies the secret: %v", composition.Warnings)
	}
}
