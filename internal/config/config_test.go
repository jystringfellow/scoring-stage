package config

import (
	"os"
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

func book(t *testing.T, dir, name, body string) string {
	t.Helper()
	testutil.WriteFile(t, filepath.Join(dir, name, "book.toml"), `name = "`+name+"\"\n"+dedent(body))
	return filepath.Join(dir, name)
}

func load(t *testing.T, home string) *Settings {
	t.Helper()
	settings, err := LoadSettings(home)
	if err != nil {
		t.Fatal(err)
	}
	return settings
}

func composeWith(t *testing.T, settings *Settings, selection Selection) *Composition {
	t.Helper()
	composition, err := Compose(settings, selection)
	if err != nil {
		t.Fatal(err)
	}
	return composition
}

func withBooks(t *testing.T, home string, books ...string) (*Settings, Selection) {
	t.Helper()
	return load(t, home), Selection{Books: books}
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

func TestWithoutChoicesABoxGetsTheShellBaseTheCoreKitAndOnlyTheGitHubBinding(t *testing.T) {
	settings := load(t, filepath.Join(t.TempDir(), "home"))
	composition := composeWith(t, settings, Selection{RepoNames: []string{"api"}})
	if !composition.Base.Equal(Kit{Source: DefaultBase}) {
		t.Errorf("base = %v", composition.Base)
	}
	if got := sources(composition.Kits); !reflect.DeepEqual(got, []string{filepath.Join(settings.CoreKits(), "stage")}) {
		t.Errorf("kits = %v", got)
	}
	if len(composition.Agents) != 0 || len(composition.AWSProfiles) != 0 || len(composition.Secrets) != 0 {
		t.Errorf("agents %v profiles %v secrets %v", composition.Agents, composition.AWSProfiles, composition.Secrets)
	}
	if len(composition.Bindings) != 0 {
		t.Errorf("bindings = %v", sortedKeys(composition.Bindings))
	}
}

func TestEachChosenAgentBringsItsMixinAndOnlyItsBindings(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "agents = [\"claude\", \"agy\"]\n")
	composition := composeWith(t, load(t, home), Selection{Agents: []string{"codex", "claude"}, Gh: "/usr/bin/gh"})
	want := []string{"docker.io/sbx/claude-mixin:2.1.319", "docker.io/sbx/antigravity-mixin:1.0.0", "docker.io/sbx/codex-mixin:0.167.0"}
	if got := sources(composition.Kits)[:3]; !reflect.DeepEqual(got, want) {
		t.Errorf("kits = %v", got)
	}
	if !reflect.DeepEqual(composition.Agents, []string{"claude", "agy", "codex"}) {
		t.Errorf("agents = %v", composition.Agents)
	}
	if got := sortedKeys(composition.Bindings); !reflect.DeepEqual(got, []string{"anthropic", "openai"}) {
		t.Errorf("bindings = %v", got)
	}
	if _, set := composition.Secrets["copilot"]; set {
		t.Error("copilot secret without the copilot agent")
	}
}

func TestAPinnedMixinReferenceKeepsTheAgentsBindings(t *testing.T) {
	composition := composeWith(t, load(t, filepath.Join(t.TempDir(), "home")), Selection{Agents: []string{"docker.io/sbx/copilot-mixin:9.9.9"}, Gh: "/usr/bin/gh"})
	if !reflect.DeepEqual(composition.Agents, []string{"copilot"}) || composition.Bindings["copilot"] == nil || composition.Secrets["copilot"] == nil {
		t.Fatalf("agents %v bindings %v secrets %v", composition.Agents, composition.Bindings, composition.Secrets)
	}
}

func TestUnknownAgentNamesAreErrors(t *testing.T) {
	_, err := Compose(load(t, filepath.Join(t.TempDir(), "home")), Selection{Agents: []string{"gemeni"}})
	expectError(t, err, "unknown agent gemeni (known: agy, claude, codex, copilot")
}

func TestCopilotSecretComesFromGhAndGitHubFromTheReadOnlyToken(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	token := writeToken(t, filepath.Join(home, "secrets", "github-readonly-token"), "github_pat_readonly")
	composition, err := Compose(load(t, home), Selection{Agents: []string{"copilot"}, Gh: "/opt/homebrew/bin/gh"})
	if err != nil {
		t.Fatal(err)
	}
	if got := composition.Secrets["copilot"]; !reflect.DeepEqual(got, map[string]any{"command": "/opt/homebrew/bin/gh auth token"}) {
		t.Errorf("copilot = %v", got)
	}
	if got := composition.Secrets["github"]; !reflect.DeepEqual(got, map[string]any{"command": "/bin/cat " + token}) {
		t.Errorf("github = %v", got)
	}
	if composition.Bindings["github"] == nil {
		t.Error("no github binding for the read-only token")
	}
}

func TestAnEmptyReadOnlyTokenFileIsIgnored(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "secrets", "github-readonly-token"), "")
	composition := composeWith(t, load(t, home), Selection{})
	if _, set := composition.Secrets["github"]; set {
		t.Fatal("github secret set from an empty token file")
	}
	if _, set := composition.Bindings["github"]; set {
		t.Fatal("github binding without a token, which would fall back to the global sbx secret")
	}
}

func TestGitHubNoneLeavesOutTheTokenEvenWhenTheFileExists(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	writeToken(t, filepath.Join(home, "secrets", "github-readonly-token"), "github_pat_readonly")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "github = \"none\"\n")
	settings := load(t, home)
	composition := composeWith(t, settings, Selection{})
	if composition.Secrets["github"] != nil || composition.Bindings["github"] != nil {
		t.Fatalf("secrets %v bindings %v", composition.Secrets, composition.Bindings)
	}
	if err := settings.CheckGitHubToken(); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyAFineGrainedTokenPassesTheGitHubTokenCheck(t *testing.T) {
	for token, fragment := range map[string]string{
		"":                                "no GitHub token at",
		"gho_abc":                         "gh CLI login token",
		"ghp_abc":                         "classic personal access token",
		"ghs_abc":                         "GitHub App token",
		"not-a-token":                     "does not hold a GitHub fine-grained token",
		"github_pat_11ABCDEF\ngho_second": "holds more than a single fine-grained token",
		"github_pat_11ABCDEF":             "",
	} {
		home := filepath.Join(t.TempDir(), "home")
		writeToken(t, filepath.Join(home, "secrets", "github-readonly-token"), token+"\n")
		err := load(t, home).CheckGitHubToken()
		if fragment == "" {
			if err != nil {
				t.Errorf("%q: %v", token, err)
			}
			continue
		}
		expectError(t, err, fragment)
	}
}

func writeToken(t *testing.T, path, token string) string {
	t.Helper()
	testutil.WriteFile(t, path, token)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestATokenFileOthersCanReadIsRefusedAndNeverBound(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "secrets", "github-readonly-token"), "github_pat_readonly")
	settings := load(t, home)
	expectError(t, settings.CheckGitHubToken(), "is readable by other users (mode 644)")
	if composeWith(t, settings, Selection{}).Bindings["github"] != nil {
		t.Fatal("bound a token that failed the check")
	}
}

func TestNoConfigOrBookBindingMaySendACredentialToGitHub(t *testing.T) {
	for _, domain := range []string{"github.com", "api.github.com:443", "*.github.com", "**", "uploads.github.com"} {
		home := filepath.Join(t.TempDir(), "home")
		testutil.WriteFile(t, filepath.Join(home, "config.toml"), "[bindings.gh2]\napiKey = { domains = [\""+domain+"\"] }\n")
		_, err := LoadSettings(home)
		expectError(t, err, "binding gh2 covers "+domain)
	}
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "[bindings.copilot]\napiKey = { domains = [\"copilot.github.com\", \"api.githubcopilot.com\"] }\n")
	load(t, home)
}

func TestConfigAndBooksCannotSupplyTheGitHubCredential(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "[secrets.github]\ncommand = \"gh auth token\"\n")
	_, err := LoadSettings(home)
	expectError(t, err, "the github secret and binding come only from the read-only token file")

	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "book.toml"), "name = \"b\"\n[bindings.github]\napiKey = { domains = [\"api.github.com\"] }\n")
	_, err = LoadBook(dir)
	expectError(t, err, "the github secret and binding come only from the read-only token file")
}

func TestAWSProfilesAddTheBrokerKitAndBinding(t *testing.T) {
	dir := t.TempDir()
	path := book(t, dir, "org", `
		[aws]
		profiles = ["dev", "staging"]
		region = "us-west-2"
	`)
	settings, selection := withBooks(t, filepath.Join(dir, "home"), path)
	composition := composeWith(t, settings, selection)
	brokerKit := composition.Kits[1]
	want := Kit{Source: filepath.Join(settings.CoreKits(), "aws-broker"), Args: map[string]any{"profiles": "dev staging", "region": "us-west-2"}}
	if !brokerKit.Equal(want) {
		t.Errorf("broker kit = %v", brokerKit)
	}
	if got := composition.Bindings[BrokerService]; !reflect.DeepEqual(got, map[string]any{"apiKey": map[string]any{"domains": []any{"localhost"}}}) {
		t.Errorf("binding = %v", got)
	}
}

func TestTheOnlyGrantedAWSProfileBecomesTheDefault(t *testing.T) {
	dir := t.TempDir()
	path := book(t, dir, "org", "[aws]\nprofiles = [\"dev\"]\n")
	settings, selection := withBooks(t, filepath.Join(dir, "home"), path)
	composition := composeWith(t, settings, selection)
	if composition.AWSDefaultProfile != "dev" {
		t.Errorf("default profile = %q", composition.AWSDefaultProfile)
	}
	want := Kit{Source: filepath.Join(settings.CoreKits(), "aws-broker"), Args: map[string]any{"profiles": "dev", "region": DefaultAWSRegion, "default_profile": "dev"}}
	if !composition.Kits[1].Equal(want) {
		t.Errorf("broker kit = %v", composition.Kits[1])
	}
}

func TestSeveralGrantedAWSProfilesHaveNoDefaultUnlessOneIsNamed(t *testing.T) {
	dir := t.TempDir()
	unnamed := book(t, dir, "unnamed", "[aws]\nprofiles = [\"dev\", \"staging\"]\n")
	named := book(t, dir, "named", "[aws]\nprofiles = [\"dev\", \"staging\"]\ndefault_profile = \"staging\"\n")
	home := filepath.Join(dir, "home")
	unnamedComposition := composeWith(t, load(t, home), Selection{Books: []string{unnamed}})
	if _, set := unnamedComposition.Kits[1].Args["default_profile"]; set || unnamedComposition.AWSDefaultProfile != "" {
		t.Errorf("unnamed default = %q, kit args %v", unnamedComposition.AWSDefaultProfile, unnamedComposition.Kits[1].Args)
	}
	composition := composeWith(t, load(t, home), Selection{Books: []string{named}})
	if composition.AWSDefaultProfile != "staging" || composition.Kits[1].Args["default_profile"] != "staging" {
		t.Errorf("named default = %q, kit args %v", composition.AWSDefaultProfile, composition.Kits[1].Args)
	}
}

func TestAWSDefaultProfileMustBeGranted(t *testing.T) {
	dir := t.TempDir()
	path := book(t, dir, "org", "[aws]\nprofiles = [\"dev\"]\ndefault_profile = \"prod\"\n")
	_, err := Compose(load(t, filepath.Join(dir, "home")), Selection{Books: []string{path}})
	expectError(t, err, "AWS default profile prod is not a granted profile")
}

func TestBooksDisagreeingOnTheAWSDefaultProfileIsAnError(t *testing.T) {
	dir := t.TempDir()
	first := book(t, dir, "one", "[aws]\nprofiles = [\"dev\", \"staging\"]\ndefault_profile = \"dev\"\n")
	second := book(t, dir, "two", "[[repos]]\nmatch = \"api\"\naws = { default_profile = \"staging\" }\n")
	_, err := Compose(load(t, filepath.Join(dir, "home")), Selection{Books: []string{first, second}, RepoNames: []string{"api"}})
	expectError(t, err, "book two repo api sets AWS default profile staging, but book one set dev")
}

func TestUserConfigOverridesTheAWSDefaultProfile(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	path := book(t, dir, "org", "[aws]\nprofiles = [\"dev\"]\n")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "[aws]\nprofiles = [\"sandbox\"]\ndefault_profile = \"sandbox\"\n")
	if got := composeWith(t, load(t, home), Selection{Books: []string{path}}).AWSDefaultProfile; got != "sandbox" {
		t.Errorf("default profile = %q", got)
	}
}

func TestTheAWSProfileFlagIsGrantedAndOverridesEveryOtherDefault(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	path := book(t, dir, "org", "[aws]\nprofiles = [\"dev\"]\ndefault_profile = \"dev\"\n")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "[aws]\nprofiles = [\"sandbox\"]\ndefault_profile = \"sandbox\"\n")
	composition := composeWith(t, load(t, home), Selection{Books: []string{path}, AWSProfile: "staging"})
	if !reflect.DeepEqual(composition.AWSProfiles, []string{"dev", "sandbox", "staging"}) || composition.AWSDefaultProfile != "staging" {
		t.Errorf("profiles %v, default %q", composition.AWSProfiles, composition.AWSDefaultProfile)
	}
}

func TestBookKitsResolveAgainstTheBookAndRepoKitsApplyOnlyToMatchingRepos(t *testing.T) {
	dir := t.TempDir()
	path := book(t, dir, "org", `
		kits = ["./kits/toolchain", "docker.io/sbx/mise:1.0.0"]

		[[repos]]
		match = ["acme/api", "Acme.Api"]
		kits = [{ source = "./kits/api-rules", args = { tier = "dev" } }]
		aws = { profiles = ["api-dev"] }
	`)
	settings, selection := withBooks(t, filepath.Join(dir, "home"), path)
	selection.RepoNames = []string{"Acme.Api", "acme/api"}
	matched := composeWith(t, settings, selection)
	selection.RepoNames = []string{"web", "acme/web"}
	unmatched := composeWith(t, settings, selection)
	bookDir := Resolve(path)
	toolchain := Kit{Source: filepath.Join(bookDir, "kits/toolchain")}
	mise := Kit{Source: "docker.io/sbx/mise:1.0.0"}
	expectKits(t, matched.Kits[len(matched.Kits)-3:], []Kit{toolchain, mise, {Source: filepath.Join(bookDir, "kits/api-rules"), Args: map[string]any{"tier": "dev"}}})
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
	path := book(t, dir, "org", `
		[[repos]]
		match = "Acme.*"
		kits = ["./kits/x"]
	`)
	settings, selection := withBooks(t, filepath.Join(dir, "home"), path)
	last := func(names ...string) string {
		selection.RepoNames = names
		kits := composeWith(t, settings, selection).Kits
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

func TestBookSecretCommandsCanNameTheBookDirectory(t *testing.T) {
	dir := t.TempDir()
	path := book(t, dir, "org with space", `
		[secrets.ado]
		command = "python3 -I {book}/host/ado.py"

		[bindings.ado]
		apiKey = { domains = ["pkgs.dev.azure.com"] }
	`)
	settings, selection := withBooks(t, filepath.Join(dir, "home"), path)
	composition := composeWith(t, settings, selection)
	want := map[string]any{"command": "python3 -I '" + Resolve(path) + "'/host/ado.py"}
	if got := composition.Secrets["ado"]; !reflect.DeepEqual(got, want) {
		t.Errorf("secret = %v, want %v", got, want)
	}
	if got := composition.Bindings["ado"]; !reflect.DeepEqual(got, map[string]any{"apiKey": map[string]any{"domains": []any{"pkgs.dev.azure.com"}}}) {
		t.Errorf("binding = %v", got)
	}
}

func TestTwoBooksDisagreeingIsAnErrorRatherThanASilentWinner(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	first := book(t, dir, "one", "base = \"docker.io/org/base:1\"\n[aws]\nregion = \"us-west-2\"\n")
	second := book(t, dir, "two", "base = \"docker.io/org/base:2\"\n")
	third := book(t, dir, "three", "[aws]\nregion = \"eu-west-1\"\n")
	_, err := Compose(load(t, home), Selection{Books: []string{first, second}})
	expectError(t, err, "book two sets base docker.io/org/base:2, but book one set")
	_, err = Compose(load(t, home), Selection{Books: []string{first, third}})
	expectError(t, err, "AWS region eu-west-1")
}

func TestBooksAgreeingOnASecretIsFineButDifferingIsAnError(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	first := book(t, dir, "one", "[secrets.ado]\ncommand = \"a\"\n")
	same := book(t, dir, "same", "[secrets.ado]\ncommand = \"a\"\n")
	other := book(t, dir, "other", "[secrets.ado]\ncommand = \"b\"\n")
	if got := composeWith(t, load(t, home), Selection{Books: []string{first, same}}).Secrets["ado"]; !reflect.DeepEqual(got, map[string]any{"command": "a"}) {
		t.Errorf("secret = %v", got)
	}
	_, err := Compose(load(t, home), Selection{Books: []string{first, other}})
	expectError(t, err, "book other sets secret ado differently from book one")
}

func TestUserConfigOverridesBooksAndListsBooks(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	path := book(t, dir, "org", "base = \"docker.io/org/base:1\"\n[aws]\nprofiles = [\"dev\"]\nregion = \"us-west-2\"\n[[repos]]\nmatch = \"api\"\n")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), dedent(`
		books = ["`+path+`"]
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
	if inactive := composeWith(t, settings, Selection{RepoNames: []string{"web"}}); len(inactive.Books) != 0 {
		t.Errorf("an installed book applied without being asked for: %v", inactive.Books)
	}
	composition := composeWith(t, settings, Selection{RepoNames: []string{"api"}})
	if !reflect.DeepEqual(composition.Books, []string{"org"}) {
		t.Errorf("books = %v", composition.Books)
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

func TestBooksAreDecodedStrictly(t *testing.T) {
	for _, c := range []struct{ body, message string }{
		{"kitz = [\"x\"]\n", "unknown keys kitz"},
		{"[aws]\nprofile = [\"x\"]\n", "unknown keys profile"},
		{"[[repos]]\nkits = [\"./x\"]\n", "match must be"},
		{"[[repos]]\nmatch = \"x\"\nrules = []\n", "unknown keys rules"},
		{"kits = [{ source = \"./x\", argz = {} }]\n", "a kit is a reference string"},
		{"[[repos]]\nmatch = \"x\"\naws = { region = \"r\", zone = \"z\" }\n", "repos[0].aws: unknown keys zone"},
		{"[aws]\nprofiles = \"dev\"\n", "profiles must be a list of strings"},
		{"[aws]\ndefault_profile = [\"dev\"]\n", "default_profile must be a string"},
		{"kits = \"./x\"\n", "kits must be a list"},
		{"name = \n", "book.toml"},
	} {
		dir := t.TempDir()
		path := book(t, dir, "org", c.body)
		settings, err := LoadSettings(filepath.Join(dir, "home"))
		if err == nil {
			_, err = Compose(settings, Selection{RepoNames: []string{"x"}, Books: []string{path}})
		}
		expectError(t, err, c.message)
	}
}

func TestABookNeedsAManifestAndAName(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	testutil.WriteFile(t, filepath.Join(dir, "empty", ".keep"), "")
	_, err := Compose(load(t, home), Selection{Books: []string{filepath.Join(dir, "empty")}})
	expectError(t, err, "no book.toml")
	testutil.WriteFile(t, filepath.Join(dir, "nameless", "book.toml"), "kits = []\n")
	_, err = Compose(load(t, home), Selection{Books: []string{filepath.Join(dir, "nameless")}})
	expectError(t, err, "name is required")
}

func TestUserConfigIsDecodedStrictly(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "book = []\n")
	_, err := LoadSettings(home)
	expectError(t, err, "unknown keys book")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "[ship]\nfooter = \"x\"\n")
	_, err = LoadSettings(home)
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

func installedBook(t *testing.T, dir, name, body string) (home string) {
	t.Helper()
	path := book(t, dir, name, body)
	home = filepath.Join(dir, "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), `books = ["`+path+`"]`+"\n")
	return home
}

func TestInstalledBooksApplyOnlyWhenNamedOrMatched(t *testing.T) {
	dir := t.TempDir()
	home := installedBook(t, dir, "dotnet", "agents = [\"claude\"]\nkits = [\"docker.io/org/dotnet:1\"]\n")
	settings := load(t, home)
	if got := composeWith(t, settings, Selection{}); len(got.Books) != 0 || len(got.Agents) != 0 {
		t.Fatalf("unrequested book applied: %v %v", got.Books, got.Agents)
	}
	named := composeWith(t, settings, Selection{Books: []string{"dotnet"}})
	if !reflect.DeepEqual(named.Books, []string{"dotnet"}) || !reflect.DeepEqual(named.Agents, []string{"claude"}) {
		t.Fatalf("named book: %v %v", named.Books, named.Agents)
	}
	_, err := Compose(settings, Selection{Books: []string{"java"}})
	expectError(t, err, "--book java: no installed book has that name (installed: dotnet)")
}

func TestARepositoryNamesInstalledBooksAndAddsAgentsAndKits(t *testing.T) {
	dir := t.TempDir()
	home := installedBook(t, dir, "dotnet", "kits = [\"docker.io/org/dotnet:1\"]\n")
	repoDir := filepath.Join(dir, "repo")
	testutil.WriteFile(t, filepath.Join(repoDir, RepoConfigPath), "agents = [\"codex\"]\nbooks = [\"dotnet\"]\nkits = [\"./tools/kit\"]\n")
	repo, err := LoadRepoConfig(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	settings := load(t, home)
	composition := composeWith(t, settings, Selection{Repo: repo})
	want := []string{"docker.io/sbx/codex-mixin:0.167.0", filepath.Join(settings.CoreKits(), "stage"), "docker.io/org/dotnet:1", Resolve(filepath.Join(repoDir, "tools/kit"))}
	if got := sources(composition.Kits); !reflect.DeepEqual(got, want) {
		t.Fatalf("kits = %v\nwant %v", got, want)
	}
}

func TestARepositoryCannotUseABookThatIsNotInstalled(t *testing.T) {
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "repo")
	testutil.WriteFile(t, filepath.Join(repoDir, RepoConfigPath), "books = [\"dotnet\"]\n")
	repo, err := LoadRepoConfig(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compose(load(t, filepath.Join(dir, "home")), Selection{Repo: repo})
	expectError(t, err, "asks for book dotnet, which is not installed (installed: none)")
}

func TestARepositoryCannotActOnTheHost(t *testing.T) {
	for body, message := range map[string]string{
		"[secrets.x]\ncommand = \"curl evil\"\n":         "a repository cannot set secrets",
		"[bindings.x]\napiKey = { domains = [\"x\"] }\n": "a repository cannot set bindings",
		"[aws]\nprofiles = [\"prod\"]\n":                 "a repository cannot set aws",
		"books = [\"../evil\"]\n":                        "books are named, not located",
		"agentz = []\n":                                  "unknown keys agentz",
	} {
		repoDir := t.TempDir()
		testutil.WriteFile(t, filepath.Join(repoDir, RepoConfigPath), body)
		_, err := LoadRepoConfig(repoDir)
		expectError(t, err, message)
	}
}

func TestARepositoryWithoutConfigAsksForNothing(t *testing.T) {
	if repo, err := LoadRepoConfig(t.TempDir()); repo != nil || err != nil {
		t.Fatalf("got %v, %v", repo, err)
	}
}

func TestSaveAgentsPrependsToTheUserConfig(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "[ship]\npr_footer = \"x\"\n")
	if err := SaveAgents(home, []string{"claude", "agy"}); err != nil {
		t.Fatal(err)
	}
	settings := load(t, home)
	if got := settings.AgentEntries(); !reflect.DeepEqual(got, []string{"claude", "agy"}) || settings.PRFooter() != "x" {
		t.Fatalf("agents %v footer %q", got, settings.PRFooter())
	}
	expectError(t, SaveAgents(home, []string{"codex"}), "already sets agents")
	expectError(t, SaveAgents(t.TempDir(), []string{"cursor"}), "unknown agent cursor")
}

func TestTwoInstalledBooksCannotShareAName(t *testing.T) {
	dir := t.TempDir()
	first, second := book(t, filepath.Join(dir, "a"), "org", ""), book(t, filepath.Join(dir, "b"), "org", "")
	home := filepath.Join(dir, "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), `books = ["`+first+`", "`+second+`"]`+"\n")
	_, err := LoadSettings(home)
	expectError(t, err, "are both named org")
}

func TestOnlyAgentsWithAnInSandboxLoginCanBeCopied(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "copy_logins = [\"agy\"]\n")
	settings := load(t, home)
	if !settings.CopiesLogin("agy") || settings.CopiesLogin("claude") {
		t.Fatal("copy_logins not honoured")
	}
	files := LoginFiles("agy")
	if len(files) != 2 || !files[0].Required || files[1].Required {
		t.Fatalf("agy login files = %v", files)
	}
	for file, want := range map[LoginFile]string{files[0]: "antigravity-oauth-token", files[1]: "onboarding.json"} {
		if got := settings.SavedLogin("agy", file); got != filepath.Join(home, "logins", "agy", want) {
			t.Errorf("saved path for %s = %s", file.Path, got)
		}
	}
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "copy_logins = [\"claude\"]\n")
	_, err := LoadSettings(home)
	expectError(t, err, "copy_logins: claude cannot be copied (copyable: agy)")

	repoDir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(repoDir, RepoConfigPath), "copy_logins = [\"agy\"]\n")
	_, err = LoadRepoConfig(repoDir)
	expectError(t, err, "a repository cannot set copy_logins")
}
