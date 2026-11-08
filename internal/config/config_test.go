package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func dedent(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimLeft(line, "\t")
	}
	return strings.Join(lines, "\n")
}

func pack(t *testing.T, dir, name, body string) string {
	t.Helper()
	testutil.WriteFile(t, filepath.Join(dir, name, "pack.toml"), `name = "`+name+"\"\n"+dedent(body))
	return filepath.Join(dir, name)
}

func load(t *testing.T, home string, packs ...string) *Settings {
	t.Helper()
	settings, err := LoadSettings(home, packs)
	if err != nil {
		t.Fatal(err)
	}
	return settings
}

func compose(t *testing.T, settings *Settings, names ...string) *Composition {
	t.Helper()
	composition, err := Compose(settings, names, "")
	if err != nil {
		t.Fatal(err)
	}
	return composition
}

func sources(kits []Kit) []string {
	var list []string
	for _, kit := range kits {
		list = append(list, kit.Source)
	}
	return list
}

func expectKits(t *testing.T, got, want []Kit) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("kits %v, want %v", got, want)
	}
	for i := range got {
		if !got[i].Equal(want[i]) {
			t.Fatalf("kit %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func expectError(t *testing.T, err error, fragment string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), fragment) {
		t.Fatalf("error %v, want it to contain %q", err, fragment)
	}
}

func TestDefaultsComposeTheShellBaseAgentsAndTheCoreKit(t *testing.T) {
	settings := load(t, filepath.Join(t.TempDir(), "home"))
	composition := compose(t, settings, "api")
	if !composition.Base.Equal(Kit{Source: DefaultBase}) {
		t.Errorf("base = %v", composition.Base)
	}
	want := append(append([]string{}, DefaultAgents...), filepath.Join(settings.CoreKits(), "stage"))
	if got := sources(composition.Kits); !reflect.DeepEqual(got, want) {
		t.Errorf("kits = %v, want %v", got, want)
	}
	if len(composition.AWSProfiles) != 0 || len(composition.Secrets) != 0 {
		t.Errorf("profiles %v secrets %v", composition.AWSProfiles, composition.Secrets)
	}
	if got := sortedKeys(composition.Bindings); !reflect.DeepEqual(got, []string{"anthropic", "copilot", "github", "openai"}) {
		t.Errorf("bindings = %v", got)
	}
}

func TestCopilotSecretComesFromGhAndGitHubFromTheReadOnlyToken(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	token := testutil.WriteFile(t, filepath.Join(home, "secrets", "github-readonly-token"), "ghp_readonly")
	composition, err := Compose(load(t, home), nil, "/opt/homebrew/bin/gh")
	if err != nil {
		t.Fatal(err)
	}
	if got := composition.Secrets["copilot"]; !reflect.DeepEqual(got, map[string]any{"command": "/opt/homebrew/bin/gh auth token"}) {
		t.Errorf("copilot = %v", got)
	}
	if got := composition.Secrets["github"]; !reflect.DeepEqual(got, map[string]any{"command": "/bin/cat " + token}) {
		t.Errorf("github = %v", got)
	}
}

func TestAnEmptyReadOnlyTokenFileIsIgnored(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "secrets", "github-readonly-token"), "")
	if _, set := compose(t, load(t, home)).Secrets["github"]; set {
		t.Fatal("github secret set from an empty token file")
	}
}

func TestAWSProfilesAddTheBrokerKitAndBinding(t *testing.T) {
	dir := t.TempDir()
	path := pack(t, dir, "org", `
		[aws]
		profiles = ["dev", "staging"]
		region = "us-west-2"
	`)
	settings := load(t, filepath.Join(dir, "home"), path)
	composition := compose(t, settings)
	brokerKit := composition.Kits[len(DefaultAgents)+1]
	want := Kit{Source: filepath.Join(settings.CoreKits(), "aws-broker"), Args: map[string]any{"profiles": "dev staging", "region": "us-west-2"}}
	if !brokerKit.Equal(want) {
		t.Errorf("broker kit = %v", brokerKit)
	}
	if got := composition.Bindings[BrokerService]; !reflect.DeepEqual(got, map[string]any{"apiKey": map[string]any{"domains": []any{"localhost"}}}) {
		t.Errorf("binding = %v", got)
	}
}

func TestPackKitsResolveAgainstThePackAndRepoKitsApplyOnlyToMatchingRepos(t *testing.T) {
	dir := t.TempDir()
	path := pack(t, dir, "org", `
		kits = ["./kits/toolchain", "docker.io/sbx/mise:1.0.0"]

		[[repos]]
		match = ["acme/api", "Acme.Api"]
		kits = [{ source = "./kits/api-rules", args = { tier = "dev" } }]
		aws = { profiles = ["api-dev"] }
	`)
	settings := load(t, filepath.Join(dir, "home"), path)
	matched := compose(t, settings, "Acme.Api", "acme/api")
	unmatched := compose(t, settings, "web", "acme/web")
	packDir := Resolve(path)
	toolchain := Kit{Source: filepath.Join(packDir, "kits/toolchain")}
	mise := Kit{Source: "docker.io/sbx/mise:1.0.0"}
	expectKits(t, matched.Kits[len(matched.Kits)-3:], []Kit{toolchain, mise, {Source: filepath.Join(packDir, "kits/api-rules"), Args: map[string]any{"tier": "dev"}}})
	if !reflect.DeepEqual(matched.AWSProfiles, []string{"api-dev"}) {
		t.Errorf("matched profiles = %v", matched.AWSProfiles)
	}
	expectKits(t, unmatched.Kits[len(unmatched.Kits)-2:], []Kit{toolchain, mise})
	if len(unmatched.AWSProfiles) != 0 {
		t.Errorf("unmatched profiles = %v", unmatched.AWSProfiles)
	}
}

func TestRepoMatchIsCaseSensitiveGlob(t *testing.T) {
	dir := t.TempDir()
	path := pack(t, dir, "org", `
		[[repos]]
		match = "Acme.*"
		kits = ["./kits/x"]
	`)
	settings := load(t, filepath.Join(dir, "home"), path)
	last := func(names ...string) string {
		kits := compose(t, settings, names...).Kits
		return kits[len(kits)-1].Source
	}
	if !strings.HasSuffix(last("Acme.Clients"), "kits/x") {
		t.Error("Acme.* should match Acme.Clients")
	}
	if strings.HasSuffix(last("acme.clients"), "kits/x") {
		t.Error("Acme.* should not match acme.clients")
	}
}

func TestFnmatchStarsCrossSlashes(t *testing.T) {
	for _, c := range []struct {
		name, pattern string
		want          bool
	}{
		{"acme/api", "*", true},
		{"acme/api", "acme/*", true},
		{"acme/api", "acme/ap?", true},
		{"acme/api", "acme/[a-b]pi", true},
		{"acme/api", "acme/[!a]pi", false},
		{"acme.api", "acme?api", true},
		{"acme+api", "acme+api", true},
		{"acmexapi", "acme.api", false},
		{"a[b", "a[b", true},
	} {
		if got := Fnmatch(c.name, c.pattern); got != c.want {
			t.Errorf("Fnmatch(%q, %q) = %v", c.name, c.pattern, got)
		}
	}
}

func TestPackSecretCommandsCanNameThePackDirectory(t *testing.T) {
	dir := t.TempDir()
	path := pack(t, dir, "org with space", `
		[secrets.ado]
		command = "python3 -I {pack}/host/ado.py"

		[bindings.ado]
		apiKey = { domains = ["pkgs.dev.azure.com"] }
	`)
	composition := compose(t, load(t, filepath.Join(dir, "home"), path))
	want := map[string]any{"command": "python3 -I '" + Resolve(path) + "'/host/ado.py"}
	if got := composition.Secrets["ado"]; !reflect.DeepEqual(got, want) {
		t.Errorf("secret = %v, want %v", got, want)
	}
	if got := composition.Bindings["ado"]; !reflect.DeepEqual(got, map[string]any{"apiKey": map[string]any{"domains": []any{"pkgs.dev.azure.com"}}}) {
		t.Errorf("binding = %v", got)
	}
}

func TestTwoPacksDisagreeingIsAnErrorRatherThanASilentWinner(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	first := pack(t, dir, "one", "base = \"docker.io/org/base:1\"\n[aws]\nregion = \"us-west-2\"\n")
	second := pack(t, dir, "two", "base = \"docker.io/org/base:2\"\n")
	third := pack(t, dir, "three", "[aws]\nregion = \"eu-west-1\"\n")
	_, err := Compose(load(t, home, first, second), nil, "")
	expectError(t, err, "pack two sets base docker.io/org/base:2, but pack one set")
	_, err = Compose(load(t, home, first, third), nil, "")
	expectError(t, err, "AWS region eu-west-1")
}

func TestPacksAgreeingOnASecretIsFineButDifferingIsAnError(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	first := pack(t, dir, "one", "[secrets.ado]\ncommand = \"a\"\n")
	same := pack(t, dir, "same", "[secrets.ado]\ncommand = \"a\"\n")
	other := pack(t, dir, "other", "[secrets.ado]\ncommand = \"b\"\n")
	if got := compose(t, load(t, home, first, same)).Secrets["ado"]; !reflect.DeepEqual(got, map[string]any{"command": "a"}) {
		t.Errorf("secret = %v", got)
	}
	_, err := Compose(load(t, home, first, other), nil, "")
	expectError(t, err, "pack other sets secret ado differently from pack one")
}

func TestUserConfigOverridesPacksAndListsPacks(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	path := pack(t, dir, "org", "base = \"docker.io/org/base:1\"\n[aws]\nprofiles = [\"dev\"]\nregion = \"us-west-2\"\n")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), dedent(`
		packs = ["`+path+`"]
		base = "docker.io/me/base:9"
		agents = ["docker.io/sbx/claude-mixin:2.1.319"]
		kits = ["./my-kit"]

		[aws]
		profiles = ["sandbox"]
		region = "eu-west-1"

		[bindings.openai]
		apiKey = { domains = ["api.openai.com"] }

		[ship]
		pr_footer = "[Robots on behalf of Someone]"
	`))
	settings := load(t, home)
	composition := compose(t, settings)
	if !reflect.DeepEqual(composition.Packs, []string{"org"}) {
		t.Errorf("packs = %v", composition.Packs)
	}
	if !composition.Base.Equal(Kit{Source: "docker.io/me/base:9"}) {
		t.Errorf("base = %v", composition.Base)
	}
	if !composition.Kits[0].Equal(Kit{Source: "docker.io/sbx/claude-mixin:2.1.319"}) {
		t.Errorf("first kit = %v", composition.Kits[0])
	}
	if got := composition.Kits[len(composition.Kits)-1]; !got.Equal(Kit{Source: Resolve(filepath.Join(home, "my-kit"))}) {
		t.Errorf("last kit = %v", got)
	}
	if !reflect.DeepEqual(composition.AWSProfiles, []string{"dev", "sandbox"}) || composition.AWSRegion != "eu-west-1" {
		t.Errorf("aws = %v %s", composition.AWSProfiles, composition.AWSRegion)
	}
	if got := composition.Bindings["openai"]; !reflect.DeepEqual(got, map[string]any{"apiKey": map[string]any{"domains": []any{"api.openai.com"}}}) {
		t.Errorf("openai binding = %v", got)
	}
	if settings.PRFooter() != "[Robots on behalf of Someone]" {
		t.Errorf("footer = %q", settings.PRFooter())
	}
}

func TestAnEmptyAgentsListMeansNoAgentKits(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "agents = []\n")
	settings := load(t, home)
	if got := sources(compose(t, settings).Kits); !reflect.DeepEqual(got, []string{filepath.Join(settings.CoreKits(), "stage")}) {
		t.Fatalf("kits = %v", got)
	}
}

func TestPacksAreDecodedStrictly(t *testing.T) {
	for _, c := range []struct{ body, message string }{
		{"kitz = [\"x\"]\n", "unknown keys kitz"},
		{"[aws]\nprofile = [\"x\"]\n", "unknown keys profile"},
		{"[[repos]]\nkits = [\"./x\"]\n", "match must be"},
		{"[[repos]]\nmatch = \"x\"\nrules = []\n", "unknown keys rules"},
		{"kits = [{ source = \"./x\", argz = {} }]\n", "a kit is a reference string"},
		{"[[repos]]\nmatch = \"x\"\naws = { region = \"r\", zone = \"z\" }\n", "repos[0].aws: unknown keys zone"},
		{"[aws]\nprofiles = \"dev\"\n", "profiles must be a list of strings"},
		{"kits = \"./x\"\n", "kits must be a list"},
		{"name = \n", "pack.toml"},
	} {
		dir := t.TempDir()
		path := pack(t, dir, "org", c.body)
		settings, err := LoadSettings(filepath.Join(dir, "home"), []string{path})
		if err == nil {
			_, err = Compose(settings, []string{"x"}, "")
		}
		expectError(t, err, c.message)
	}
}

func TestAPackNeedsAManifestAndAName(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	testutil.WriteFile(t, filepath.Join(dir, "empty", ".keep"), "")
	_, err := LoadSettings(home, []string{filepath.Join(dir, "empty")})
	expectError(t, err, "no pack.toml")
	testutil.WriteFile(t, filepath.Join(dir, "nameless", "pack.toml"), "kits = []\n")
	_, err = LoadSettings(home, []string{filepath.Join(dir, "nameless")})
	expectError(t, err, "name is required")
}

func TestUserConfigIsDecodedStrictly(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "pack = []\n")
	_, err := LoadSettings(home, nil)
	expectError(t, err, "unknown keys pack")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "[ship]\nfooter = \"x\"\n")
	_, err = LoadSettings(home, nil)
	expectError(t, err, "config.toml [ship]: unknown keys footer")
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"":             "''",
		"/usr/bin/gh":  "/usr/bin/gh",
		"a b":          "'a b'",
		"it's":         `'it'"'"'s'`,
		"x@y%z+=:,./-": "x@y%z+=:,./-",
	} {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestStageHomeDefaultsUnderTheUserHomeAndHonoursStageHome(t *testing.T) {
	user := t.TempDir()
	t.Setenv("HOME", user)
	t.Setenv("STAGE_HOME", "")
	if got := StageHome(); got != filepath.Join(Resolve(user), ".scoring-stage") {
		t.Errorf("default = %s", got)
	}
	t.Setenv("STAGE_HOME", "~/elsewhere")
	if got := StageHome(); got != filepath.Join(Resolve(user), "elsewhere") {
		t.Errorf("override = %s", got)
	}
}
