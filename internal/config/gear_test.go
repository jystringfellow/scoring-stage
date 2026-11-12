package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func userHome(t *testing.T) string {
	t.Helper()
	home := Resolve(t.TempDir())
	t.Setenv("HOME", home)
	return home
}

func checkedGear(t *testing.T, body string) ([]Gear, error) {
	t.Helper()
	settings, err := gearConfig(t, body)
	if err != nil {
		t.Fatalf("the config did not load: %v", err)
	}
	return settings.CheckedGear()
}

func gearConfig(t *testing.T, body string) (*Settings, error) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "stage-home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), body)
	return LoadSettings(home)
}

func TestGearIsAHomePathOrAFromAndToPair(t *testing.T) {
	user := userHome(t)
	elsewhere := Resolve(t.TempDir())

	gear, err := checkedGear(t, `gear = ["~/.tool/config.toml", { from = "`+elsewhere+`/presets", to = "~/.tool/presets" }, { from = "~/notes.md", to = "/srv/notes.md" }]`+"\n")
	if err != nil {
		t.Fatal(err)
	}

	want := []Gear{
		{From: "~/.tool/config.toml", To: "~/.tool/config.toml", HostPath: filepath.Join(user, ".tool", "config.toml")},
		{From: elsewhere + "/presets", To: "~/.tool/presets", HostPath: filepath.Join(elsewhere, "presets")},
		{From: "~/notes.md", To: "/srv/notes.md", HostPath: filepath.Join(user, "notes.md")},
	}
	if !reflect.DeepEqual(gear, want) {
		t.Errorf("gear = %+v\nwant %+v", gear, want)
	}
}

func TestAGearEntryIsRefusedUnlessItsPathsAreCleanAndRooted(t *testing.T) {
	userHome(t)
	for entry, fragment := range map[string]string{
		`"/etc/tool.conf"`:   "must start with ~/",
		`"tool/config.toml"`: "must start with ~/",
		`"~"`:                "must start with ~/",
		`"~/"`:               "has no .., doubled or trailing slash",
		`"~/../etc/passwd"`:  "has no .., doubled or trailing slash",
		`"~/.tool/"`:         "has no .., doubled or trailing slash",
		`"~/.tool//config"`:  "has no .., doubled or trailing slash",
		`"~/.to\u0007ol"`:    "has no .., doubled or trailing slash",
		`{ from = "~/a" }`:   "a gear entry is",
		`{ to = "~/a" }`:     "a gear entry is",
		`{ from = "~/a", to = "~/b", mode = "600" }`: "a gear entry is",
		`{ from = 1, to = "~/b" }`:                   "a gear entry is",
		`7`:                                          "a gear entry is",
		`{ from = "a", to = "~/b" }`:                 "from a must be a path",
		`{ from = "~/a/../b", to = "~/b" }`:          "from ~/a/../b must be a path",
		`{ from = "~/a", to = "b" }`:                 "to b must be a path",
		`{ from = "~/a", to = "~/../b" }`:            "to ~/../b must be a path",
		`{ from = "~/a", to = "/" }`:                 "to / must be a path",
		`"~/.tool/config", { from = "~/b", to = "~/.tool/config" }`: "are both copied to ~/.tool/config",
	} {
		_, err := gearConfig(t, "gear = ["+entry+"]\n")
		if err == nil || !strings.Contains(err.Error(), "gear:") || !strings.Contains(err.Error(), fragment) {
			t.Errorf("gear = [%s]: error %v, want a gear error containing %q", entry, err, fragment)
		}
	}
	if _, err := gearConfig(t, "gear = \"~/.tool\"\n"); err == nil || !strings.Contains(err.Error(), "gear must be a list") {
		t.Errorf("gear as a string: error %v", err)
	}
}

func TestOnlyTheUsersConfigMayListGear(t *testing.T) {
	userHome(t)
	const reason = "only ~/.scoring-stage/config.toml may list it"
	for where, load := range map[string]func(t *testing.T) error{
		"a repository cannot list gear": func(t *testing.T) error {
			repoDir := filepath.Join(t.TempDir(), "repo")
			testutil.WriteFile(t, filepath.Join(repoDir, RepoConfigPath), "gear = [\"~/.tool/config.toml\"]\n")
			_, err := LoadRepoConfig(repoDir)
			return err
		},
		"a book cannot list gear": func(t *testing.T) error {
			_, err := LoadBook(BuiltinAgents(), book(t, t.TempDir(), "carrier", "gear = [\"~/.tool/config.toml\"]\n"))
			return err
		},
		"a [[repos]] rule cannot list gear": func(t *testing.T) error {
			_, err := LoadBook(BuiltinAgents(), book(t, t.TempDir(), "carrier", "[[repos]]\nmatch = \"acme/*\"\ngear = [\"~/.tool/config.toml\"]\n"))
			return err
		},
	} {
		err := load(t)
		if err == nil || !strings.Contains(err.Error(), where) || !strings.Contains(err.Error(), reason) {
			t.Errorf("error %v, want %q and %q", err, where, reason)
		}
	}
}

func expectGearRefused(t *testing.T, entry, fragment string) {
	t.Helper()
	_, err := checkedGear(t, "gear = ["+entry+"]\n")
	if err == nil || !strings.Contains(err.Error(), "gear:") || !strings.Contains(err.Error(), fragment) {
		t.Errorf("gear = [%s]: error %v, want a gear error containing %q", entry, err, fragment)
	}
}

func TestGearCannotComeFromStagesOwnDirectoryOrAKnownCredentialLocation(t *testing.T) {
	user := userHome(t)
	stageHome := filepath.Join(user, ".scoring-stage")
	testutil.WriteFile(t, filepath.Join(stageHome, "secrets", "github", "acme.token"), "token")
	testutil.WriteFile(t, filepath.Join(user, ".ssh", "id_ed25519"), "key")
	if err := os.Symlink(filepath.Join(user, ".ssh"), filepath.Join(user, "keys")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(stageHome, "secrets"), filepath.Join(user, "mine")); err != nil {
		t.Fatal(err)
	}
	check := func(body string) error {
		testutil.WriteFile(t, filepath.Join(stageHome, "config.toml"), body)
		settings, err := LoadSettings(stageHome)
		if err != nil {
			t.Fatalf("a config whose gear stage up refuses did not load, which stops every other command: %v", err)
		}
		_, err = settings.CheckedGear()
		return err
	}
	for entry, fragment := range map[string]string{
		`"~/.scoring-stage/secrets/github/acme.token"`:   "stage's own directory",
		`"~/.scoring-stage"`:                             "stage's own directory",
		`"~/mine/github/acme.token"`:                     "stage's own directory",
		`{ from = "` + user + `", to = "~/everything" }`: "stage's own directory",
		`"~/.ssh/id_ed25519"`:                            "~/.ssh",
		`"~/.SSH/id_ed25519"`:                            "~/.ssh",
		`"~/keys/id_ed25519"`:                            "~/.ssh",
		`"~/.aws"`:                                       "~/.aws",
		`"~/.config"`:                                    "~/.config/",
		`"~/.docker"`:                                    "~/.docker/config.json",
		`{ from = "~/.gemini/antigravity-cli", to = "~/agy" }`: "~/.gemini/antigravity-cli/antigravity-oauth-token",
		`"~/.config/github-copilot/hosts.json"`:                "~/.config/github-copilot",
		`"~/.terraform.d"`:                                     "~/.terraform.d",
		`"~/.cargo"`:                                           "~/.cargo/credentials",
		`"~/.cargo/credentials.toml"`:                          "~/.cargo/credentials.toml",
		`"~/.gem/credentials"`:                                 "~/.gem/credentials",
		`"~/.m2/settings.xml"`:                                 "~/.m2/settings.xml",
		`"~/.vault-token"`:                                     "~/.vault-token",
		`"~/.pgpass"`:                                          "~/.pgpass",
		`"~/.my.cnf"`:                                          "~/.my.cnf",
		`"~/.config/rclone/rclone.conf"`:                       "~/.config/rclone",
		`"~/.config/op"`:                                       "~/.config/op",
		`"~/.password-store/work"`:                             "~/.password-store",
		`"~/.config/containers"`:                               "~/.config/containers/auth.json",
		`"~/.local/share"`:                                     "~/.local/share/",
		`"~/Library/Keychains/login.keychain-db"`:              "~/Library/Keychains",
		`"~/.netrc"`:                                           "~/.netrc",
		`"~/.npmrc"`:                                           "~/.npmrc",
		`"~/.pypirc"`:                                          "~/.pypirc",
		`"~/.git-credentials"`:                                 "~/.git-credentials",
		`"~/.bash_history"`:                                    "~/.bash_history",
		`"~/.zsh_history"`:                                     "~/.zsh_history",
		`"~/.local/share/fish/fish_history"`:                   "~/.local/share/fish/fish_history",
	} {
		if err := check("gear = [" + entry + "]\n"); err == nil || !strings.Contains(err.Error(), "gear:") || !strings.Contains(err.Error(), fragment) {
			t.Errorf("gear = [%s]: error %v, want a gear error containing %q", entry, err, fragment)
		}
	}
	if err := check("gear = [\"~/.docker/daemon.json\", \"~/.config/tool\", \"~/.sshrc\", \"~/.cargo/config.toml\", \"~/.m2/toolchains.xml\"]\n"); err != nil {
		t.Errorf("paths beside a credential location were refused: %v", err)
	}
}

func TestGearCannotCarryWhatALinkInsideACredentialDirectoryPointsTo(t *testing.T) {
	user := userHome(t)
	testutil.WriteFile(t, filepath.Join(user, "dotfiles", "aws-credentials"), "secret")
	testutil.WriteFile(t, filepath.Join(user, "dotfiles", "vimrc"), "set nu")
	testutil.WriteFile(t, filepath.Join(user, "vault", "keys", "id_ed25519"), "key")
	testutil.WriteFile(t, filepath.Join(user, ".aws", "config"), "")
	testutil.WriteFile(t, filepath.Join(user, ".ssh", "deep", "known_hosts"), "")
	for link, target := range map[string]string{
		filepath.Join(user, ".aws", "credentials"):  filepath.Join(user, "dotfiles", "aws-credentials"),
		filepath.Join(user, ".ssh", "deep", "keys"): filepath.Join(user, "vault", "keys"),
		filepath.Join(user, ".ssh", "dangling"):     filepath.Join(user, "gone"),
		filepath.Join(user, ".ssh", "inner"):        filepath.Join(user, ".ssh", "deep"),
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}

	for entry, fragment := range map[string]string{
		`"~/dotfiles"`:                 filepath.Join(user, ".aws", "credentials") + " links to",
		`"~/dotfiles/aws-credentials"`: filepath.Join(user, ".aws", "credentials") + " links to",
		`"~/vault"`:                    filepath.Join(user, ".ssh", "deep", "keys") + " links to",
		`"~/vault/keys/id_ed25519"`:    filepath.Join(user, ".ssh", "deep", "keys") + " links to",
	} {
		expectGearRefused(t, entry, fragment)
	}
	if _, err := checkedGear(t, "gear = [\"~/dotfiles/vimrc\"]\n"); err != nil {
		t.Errorf("a file beside a linked credential was refused: %v", err)
	}
}

func TestCredentialLocationsFollowTheVariablesThatMoveThem(t *testing.T) {
	user := userHome(t)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(user, "cloud", "aws-keys"))
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(user, "cloud", "aws-config"))
	t.Setenv("KUBECONFIG", filepath.Join(user, "k8s", "dev.yaml")+string(os.PathListSeparator)+filepath.Join(user, "k8s", "prod.yaml"))
	t.Setenv("GNUPGHOME", filepath.Join(user, "keyring"))
	t.Setenv("DOCKER_CONFIG", filepath.Join(user, "containers"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(user, "xdg"))
	t.Setenv("HISTFILE", filepath.Join(user, "shell", "history"))

	for entry, fragment := range map[string]string{
		`"~/cloud/aws-keys"`:         "$AWS_SHARED_CREDENTIALS_FILE",
		`"~/cloud"`:                  "$AWS_",
		`"~/cloud/aws-config"`:       "$AWS_CONFIG_FILE",
		`"~/k8s/dev.yaml"`:           "$KUBECONFIG",
		`"~/k8s/prod.yaml"`:          "$KUBECONFIG",
		`"~/keyring/private-keys"`:   "$GNUPGHOME",
		`"~/containers/config.json"`: "$DOCKER_CONFIG/config.json",
		`"~/xdg/gh/hosts.yml"`:       "$XDG_CONFIG_HOME/gh",
		`"~/xdg"`:                    "$XDG_CONFIG_HOME/",
		`"~/shell/history"`:          "$HISTFILE",
		`"~/.config/gh"`:             "~/.config/gh",
	} {
		expectGearRefused(t, entry, fragment)
	}
	if _, err := checkedGear(t, "gear = [\"~/xdg/tool/config.toml\", \"~/containers/daemon.json\", \"~/k8s/notes.md\"]\n"); err != nil {
		t.Errorf("paths beside a moved credential location were refused: %v", err)
	}
}

func TestGearIsRefusedWhenTheHomeDirectoryIsUnknown(t *testing.T) {
	source := filepath.Join(Resolve(t.TempDir()), "config.toml")
	t.Setenv("HOME", "")
	_, err := checkedGear(t, `gear = [{ from = "`+source+`", to = "~/.tool/config.toml" }]`+"\n")
	expectError(t, err, "cannot find your home directory")
}

func TestGearCannotLandOnAnAgentsLoginFilesOrInTheWorkspace(t *testing.T) {
	userHome(t)
	for _, to := range []string{
		"~/.gemini/antigravity-cli/antigravity-oauth-token",
		"/home/agent/.gemini/antigravity-cli/cache/onboarding.json",
		"~/.gemini",
		"~/.gemini/antigravity-cli/cache/onboarding.json/under",
	} {
		_, err := gearConfig(t, `gear = [{ from = "~/mine", to = "`+to+`" }]`+"\n")
		if err == nil || !strings.Contains(err.Error(), "gear:") || !strings.Contains(err.Error(), "agy's login files") {
			t.Errorf("to = %s: error %v, want a gear error naming agy's login files", to, err)
		}
	}
	if _, err := gearConfig(t, `gear = [{ from = "~/mine", to = "~/.gemini/settings.json" }]`+"\n"); err != nil {
		t.Errorf("a destination beside the login files was refused: %v", err)
	}

	workspace := "/home/someone/.scoring-stage/tasks/fix/src"
	for to, lands := range map[string]bool{
		workspace:                 true,
		workspace + "/.tool":      true,
		"/home/someone":           true,
		"/home/someone/elsewhere": false,
		"~/.tool":                 false,
	} {
		if got := GearLandsIn(Gear{To: to}, workspace); got != lands {
			t.Errorf("GearLandsIn(%s) = %v, want %v", to, got, lands)
		}
	}
	if !GearLandsIn(Gear{To: "~/work/api"}, "/home/agent/work/api/src") {
		t.Error("a ~/ destination was not read as under the sandbox user's home")
	}
}

func TestManagedSettingsCanRefuseGearWithoutStoppingTheConfigFromLoading(t *testing.T) {
	userHome(t)
	managedFiles(t, "allow_gear = true\n", "allow_gear = false\n", "allow_gear = true\n")

	_, err := checkedGear(t, "gear = [\"~/.tool/config.toml\"]\n")
	expectError(t, err, "gear is refused by")

	settings, err := gearConfig(t, "")
	if err != nil {
		t.Fatalf("a config without gear was refused: %v", err)
	}
	if gear, err := settings.CheckedGear(); err != nil || len(gear) != 0 {
		t.Errorf("no gear, yet CheckedGear = %v, %v", gear, err)
	}
	if summary := strings.Join(settings.Managed.Summary(), "\n"); !strings.Contains(summary, "  gear: refused") {
		t.Errorf("summary lacks the refusal:\n%s", summary)
	}

	managedFiles(t, "allow_gear = \"no\"\n")
	_, err = LoadManaged()
	expectError(t, err, "allow_gear must be true or false")

	managedFiles(t, "repo_kits = \"deny\"\n")
	if _, err := checkedGear(t, "gear = [\"~/.tool/config.toml\"]\n"); err != nil {
		t.Errorf("managed settings that do not mention gear refused it: %v", err)
	}
}
