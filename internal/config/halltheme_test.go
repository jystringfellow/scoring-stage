package config

import (
	"strings"
	"testing"
)

func TestTheHallIsDarkWithNeonGreenAndDeepPurpleUnlessTheUserSaysOtherwise(t *testing.T) {
	settings, err := hallSettings(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := (HallTheme{Mode: "dark", Accent: "#39FF14", Second: "#5B21B6"}); settings.Hall.Theme != want {
		t.Errorf("theme %+v, want %+v", settings.Hall.Theme, want)
	}
}

func TestTheHallThemeIsReadFromTheUsersConfig(t *testing.T) {
	settings, err := hallSettings(t, "[hall.theme]\nmode = \"light\"\naccent = \"#22d3ee\"\nsecond = \"#1E3A8A\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := (HallTheme{Mode: "light", Accent: "#22D3EE", Second: "#1E3A8A"}); settings.Hall.Theme != want {
		t.Errorf("theme %+v, want %+v", settings.Hall.Theme, want)
	}
	settings, err = hallSettings(t, "[hall.theme]\nmode = \"auto\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := (HallTheme{Mode: "auto", Accent: "#39FF14", Second: "#5B21B6"}); settings.Hall.Theme != want {
		t.Errorf("theme %+v, want %+v", settings.Hall.Theme, want)
	}
}

func TestTheHallThemeRefusesWhatItCannotDraw(t *testing.T) {
	for config, fragment := range map[string]string{
		"[hall]\ntheme = 1\n":                         "must be a table",
		"[hall.theme]\nmode = \"dim\"\n":              "mode",
		"[hall.theme]\nmode = 1\n":                    "mode",
		"[hall.theme]\naccent = \"green\"\n":          "accent",
		"[hall.theme]\naccent = \"#39FF1\"\n":         "accent",
		"[hall.theme]\naccent = \"#39FF14;x\"\n":      "accent",
		"[hall.theme]\nsecond = \"5B21B6\"\n":         "second",
		"[hall.theme]\nsecond = 5\n":                  "second",
		"[hall.theme]\nfont = \"serif\"\n":            "unknown keys font",
		"[hall.theme]\naccent = \"#39FF14\\u0000\"\n": "accent",
	} {
		_, err := hallSettings(t, config)
		if err == nil || !strings.Contains(err.Error(), "[hall] theme") || !strings.Contains(err.Error(), fragment) {
			t.Errorf("%q: error %v, want a [hall] theme error containing %q", config, err, fragment)
		}
	}
}
