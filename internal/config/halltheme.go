package config

import (
	"regexp"
	"slices"
	"strings"
)

type HallTheme struct {
	Mode   string
	Accent string
	Second string
}

var (
	defaultHallTheme = HallTheme{Mode: "dark", Accent: "#39FF14", Second: "#5B21B6"}
	hallThemeModes   = []string{"dark", "light", "auto"}
	hallThemeColor   = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

func decodeHallTheme(value any, where string) (HallTheme, error) {
	theme := defaultHallTheme
	where += " theme"
	table, ok := value.(map[string]any)
	if !ok {
		return theme, errorf("%s: must be a table", where)
	}
	if err := checkKeys(table, []string{"mode", "accent", "second"}, where); err != nil {
		return theme, err
	}
	if raw, present := table["mode"]; present {
		mode, ok := raw.(string)
		if !ok || !slices.Contains(hallThemeModes, mode) {
			return theme, errorf("%s: mode must be one of %s", where, strings.Join(hallThemeModes, ", "))
		}
		theme.Mode = mode
	}
	for key, target := range map[string]*string{"accent": &theme.Accent, "second": &theme.Second} {
		raw, present := table[key]
		if !present {
			continue
		}
		color, ok := raw.(string)
		if !ok || !hallThemeColor.MatchString(color) {
			return theme, errorf("%s: %s must be a color such as \"#39FF14\"", where, key)
		}
		*target = strings.ToUpper(color)
	}
	return theme, nil
}
