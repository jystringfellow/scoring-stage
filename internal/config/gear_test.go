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

func gearConfig(t *testing.T, body string) (*Settings, error) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "stage-home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), body)
	return LoadSettings(home)
}

func TestGearIsAHomePathOrAFromAndToPair(t *testing.T) {
	user := userHome(t)
	elsewhere := Resolve(t.TempDir())

	settings, err := gearConfig(t, `gear = ["~/.tool/config.toml", { from = "`+elsewhere+`/presets", to = "~/.tool/presets" }, { from = "~/notes.md", to = "/srv/notes.md" }]`+"\n")
	if err != nil {
		t.Fatal(err)
	}

	want := []Gear{
		{From: "~/.tool/config.toml", To: "~/.tool/config.toml", HostPath: filepath.Join(user, ".tool", "config.toml")},
		{From: elsewhere + "/presets", To: "~/.tool/presets", HostPath: filepath.Join(elsewhere, "presets")},
		{From: "~/notes.md", To: "/srv/notes.md", HostPath: filepath.Join(user, "notes.md")},
	}
	if !reflect.DeepEqual(settings.Gear, want) {
		t.Errorf("gear = %+v\nwant %+v", settings.Gear, want)
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
	for entry, fragment := range map[string]string{
		`"~/.scoring-stage/secrets/github/acme.token"`:   "stage's own directory",
		`"~/.scoring-stage"`:                             "stage's own directory",
		`"~/mine/github/acme.token"`:                     "stage's own directory",
		`{ from = "` + user + `", to = "~/everything" }`: "stage's own directory",
		`"~/.ssh/id_ed25519"`:                            "~/.ssh",
		`"~/.SSH/id_ed25519"`:                            "~/.ssh",
		`"~/keys/id_ed25519"`:                            "~/.ssh",
		`"~/.aws"`:                                       "~/.aws",
		`"~/.config"`:                                    "~/.config/git/credentials",
		`"~/.docker"`:                                    "~/.docker/config.json",
		`"~/.gemini/antigravity-cli"`:                    "~/.gemini/antigravity-cli/antigravity-oauth-token",
	} {
		home := stageHome
		testutil.WriteFile(t, filepath.Join(home, "config.toml"), "gear = ["+entry+"]\n")
		_, err := LoadSettings(home)
		if err == nil || !strings.Contains(err.Error(), "gear:") || !strings.Contains(err.Error(), fragment) {
			t.Errorf("gear = [%s]: error %v, want a gear error containing %q", entry, err, fragment)
		}
	}
	testutil.WriteFile(t, filepath.Join(stageHome, "config.toml"), "gear = [\"~/.docker/daemon.json\", \"~/.config/tool\", \"~/.sshrc\"]\n")
	if _, err := LoadSettings(stageHome); err != nil {
		t.Errorf("paths beside a credential location were refused: %v", err)
	}
}

func TestManagedSettingsCanRefuseGear(t *testing.T) {
	userHome(t)
	managedFiles(t, "allow_gear = true\n", "allow_gear = false\n", "allow_gear = true\n")

	_, err := gearConfig(t, "gear = [\"~/.tool/config.toml\"]\n")
	expectError(t, err, "gear is refused by")

	settings, err := gearConfig(t, "")
	if err != nil {
		t.Fatalf("a config without gear was refused: %v", err)
	}
	if summary := strings.Join(settings.Managed.Summary(), "\n"); !strings.Contains(summary, "  gear: refused") {
		t.Errorf("summary lacks the refusal:\n%s", summary)
	}

	managedFiles(t, "allow_gear = \"no\"\n")
	_, err = LoadManaged()
	expectError(t, err, "allow_gear must be true or false")

	managedFiles(t, "repo_kits = \"deny\"\n")
	if _, err := gearConfig(t, "gear = [\"~/.tool/config.toml\"]\n"); err != nil {
		t.Errorf("managed settings that do not mention gear refused it: %v", err)
	}
}
