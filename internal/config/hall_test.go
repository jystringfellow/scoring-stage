package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func hallSettings(t *testing.T, content string) (*Settings, error) {
	t.Helper()
	home := filepath.Join(userHome(t), ".scoring-stage")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), content)
	return LoadSettings(home)
}

func TestHallSettingsAreReadFromTheUsersConfig(t *testing.T) {
	settings, err := hallSettings(t, `[hall]
providers = ["second", "first"]
cao_ports = [9889, 9890]
[hall.provider.first]
command = ["first-status", "--format", "stage-activity"]
[hall.provider.second]
command = ["second-status"]
`)
	if err != nil {
		t.Fatal(err)
	}
	hall := settings.Hall
	if !slices.Equal(hall.Providers, []string{"second", "first"}) || !slices.Equal(hall.CAOPorts, []int{9889, 9890}) {
		t.Errorf("hall %+v", hall)
	}
	if got := hall.Commands["first"]; !slices.Equal(got, []string{"first-status", "--format", "stage-activity"}) {
		t.Errorf("first command %v", got)
	}
}

func TestWithoutHallSettingsEveryProviderRunsAndCAOIsReadOn9889(t *testing.T) {
	settings, err := hallSettings(t, "[hall.provider.zeta]\ncommand = [\"zeta\"]\n[hall.provider.alpha]\ncommand = [\"alpha\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	want := append(slices.Clone(BuiltinProviders), "alpha", "zeta")
	if !slices.Equal(settings.Hall.Providers, want) || !slices.Equal(settings.Hall.CAOPorts, []int{9889}) {
		t.Errorf("hall %+v, want providers %v on 9889", settings.Hall, want)
	}
	settings, err = hallSettings(t, "")
	if err != nil || !slices.Equal(settings.Hall.Providers, BuiltinProviders) || !slices.Equal(settings.Hall.CAOPorts, []int{9889}) {
		t.Errorf("hall %+v %v", settings.Hall, err)
	}
}

func TestOnlyTheUsersConfigMaySetTheHall(t *testing.T) {
	userHome(t)
	const reason = "only ~/.scoring-stage/config.toml may set it"
	hall := "[hall.provider.loop]\ncommand = [\"loop\"]\n"
	for where, load := range map[string]func(t *testing.T) error{
		"a repository cannot set [hall]": func(t *testing.T) error {
			repoDir := filepath.Join(t.TempDir(), "repo")
			testutil.WriteFile(t, filepath.Join(repoDir, RepoConfigPath), hall)
			_, err := LoadRepoConfig(repoDir)
			return err
		},
		"a book cannot set [hall]": func(t *testing.T) error {
			_, err := LoadBook(BuiltinAgents(), book(t, t.TempDir(), "carrier", hall))
			return err
		},
		"a [[repos]] rule cannot set [hall]": func(t *testing.T) error {
			_, err := LoadBook(BuiltinAgents(), book(t, t.TempDir(), "carrier", "[[repos]]\nmatch = \"acme/*\"\nhall = {}\n"))
			return err
		},
	} {
		err := load(t)
		if err == nil || !strings.Contains(err.Error(), where) || !strings.Contains(err.Error(), reason) {
			t.Errorf("error %v, want %q and %q", err, where, reason)
		}
	}
}

func TestHallRefusesWhatItCannotRun(t *testing.T) {
	for config, fragment := range map[string]string{
		"hall = 1\n":                                                                             "[hall] must be a table",
		"[hall]\nrefresh = 5\n":                                                                  "unknown keys refresh",
		"[hall.provider.loop]\n":                                                                 "command",
		"[hall.provider.loop]\ncommand = []\n":                                                   "command",
		"[hall.provider.loop]\ncommand = [\"\"]\n":                                               "command",
		"[hall.provider.loop]\ncommand = [\"loop\", 7]\n":                                        "command",
		"[hall.provider.loop]\ncommand = \"loop\"\n":                                             "command",
		"[hall.provider.loop]\ncommand = [\"a\\u0000\"]\n":                                       "command",
		"[hall.provider.loop]\ncommand = [\"loop\"]\nenv = {}\n":                                 "unknown keys env",
		"[hall.provider.Loop]\ncommand = [\"loop\"]\n":                                           "provider name",
		"[hall.provider.\"a b\"]\ncommand = [\"loop\"]\n":                                        "provider name",
		"[hall]\nproviders = [\"missing\"]\n":                                                    "missing is not a provider",
		"[hall]\nproviders = [\"loop\", \"loop\"]\n[hall.provider.loop]\ncommand = [\"loop\"]\n": "more than once",
		"[hall]\nproviders = \"loop\"\n":                                                         "providers",
		"[hall]\ncao_ports = [0]\n":                                                              "cao_ports",
		"[hall]\ncao_ports = [65536]\n":                                                          "cao_ports",
		"[hall]\ncao_ports = [\"9889\"]\n":                                                       "cao_ports",
		"[hall]\ncao_ports = [9889, 9889]\n":                                                     "cao_ports",
		"[hall]\ncao_ports = []\n":                                                               "cao_ports",
		"[hall]\ncao_ports = [1, 2, 3, 4, 5, 6, 7, 8, 9]\n":                                      "cao_ports",
	} {
		_, err := hallSettings(t, config)
		if err == nil || !strings.Contains(err.Error(), "[hall") || !strings.Contains(err.Error(), fragment) {
			t.Errorf("%q: error %v, want a [hall] error containing %q", config, err, fragment)
		}
	}
}

func TestABuiltinProviderNameCannotBeRedefined(t *testing.T) {
	for _, name := range BuiltinProviders {
		_, err := hallSettings(t, "[hall.provider."+name+"]\ncommand = [\"x\"]\n")
		if err == nil || !strings.Contains(err.Error(), "built in") {
			t.Errorf("%s: error %v", name, err)
		}
	}
}

func TestTheHallKeepsItsViewsThirtyDaysUnlessTheUserSaysOtherwise(t *testing.T) {
	settings, err := hallSettings(t, "")
	if err != nil || settings.Hall.Keep != 30*24*time.Hour {
		t.Fatalf("default keep %v, %v", settings.Hall.Keep, err)
	}
	for text, want := range map[string]time.Duration{`"7d"`: 7 * 24 * time.Hour, `"12h"`: 12 * time.Hour, `"0"`: 0, `"3650d"`: 3650 * 24 * time.Hour} {
		settings, err := hallSettings(t, "[hall]\nkeep = "+text+"\n")
		if err != nil || settings.Hall.Keep != want {
			t.Errorf("keep = %s: %v, %v", text, settings.Hall.Keep, err)
		}
	}
	for _, text := range []string{`"soon"`, `"-1d"`, `"3651d"`, `7`, `"7"`, `"1.5d"`} {
		if _, err := hallSettings(t, "[hall]\nkeep = "+text+"\n"); err == nil || !strings.Contains(err.Error(), "keep") {
			t.Errorf("keep = %s: error %v", text, err)
		}
	}
}
