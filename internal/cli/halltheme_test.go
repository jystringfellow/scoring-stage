package cli

import (
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/config"
)

func luminanceOf(t *testing.T, color string) float64 {
	t.Helper()
	if !regexp.MustCompile(`^#[0-9A-F]{6}$`).MatchString(color) {
		t.Fatalf("not a color: %q", color)
	}
	var total float64
	for i, weight := range []float64{0.2126, 0.7152, 0.0722} {
		channel, _ := strconv.ParseUint(color[1+2*i:3+2*i], 16, 8)
		value := float64(channel) / 255
		if value <= 0.03928 {
			value /= 12.92
		} else {
			value = math.Pow((value+0.055)/1.055, 2.4)
		}
		total += weight * value
	}
	return total
}

func contrastOf(t *testing.T, a, b string) float64 {
	t.Helper()
	x, y := luminanceOf(t, a), luminanceOf(t, b)
	return (math.Max(x, y) + 0.05) / (math.Min(x, y) + 0.05)
}

func cssTokens(t *testing.T, css, selector string) map[string]string {
	t.Helper()
	start := strings.Index(css, selector+" {")
	if start < 0 {
		t.Fatalf("no block %q in\n%s", selector, css)
	}
	block := css[start+len(selector)+2:]
	block = block[:strings.Index(block, "}")]
	tokens := map[string]string{}
	for _, declaration := range strings.Split(block, ";") {
		name, value, found := strings.Cut(strings.TrimSpace(declaration), ":")
		if found {
			tokens[strings.TrimPrefix(name, "--")] = value
		}
	}
	return tokens
}

func TestTheHallThemeKeepsTextReadableInBothModesWhateverTheColors(t *testing.T) {
	colors := []string{"#39FF14", "#5B21B6", "#FACC15", "#1E3A8A", "#FFFFFF", "#000000", "#767676", "#22D3EE", "#881337"}
	for _, accent := range colors {
		for _, second := range colors {
			css := hallThemeCSS(config.HallTheme{Mode: "auto", Accent: accent, Second: second})
			for _, selector := range []string{`:root, :root[data-theme="dark"]`, `:root[data-theme="light"]`} {
				tokens := cssTokens(t, css, selector)
				pairs := [][3]string{
					{"text", "bg", "7"}, {"text", "panel", "7"}, {"muted", "panel", "4.5"}, {"muted", "inset", "4.5"},
					{"idle", "panel", "4.5"}, {"bad", "panel", "4.5"}, {"term-fg", "term-bg", "7"},
					{"accent-ink", "panel", "4.5"}, {"accent-ink", "bg", "4.5"}, {"accent-ink", "inset", "4.5"},
					{"second-ink", "panel", "4.5"}, {"second-ink", "bg", "4.5"}, {"second-ink", "inset", "4.5"},
					{"on-accent", "accent", "4.5"}, {"on-second", "second", "4.5"},
				}
				for _, pair := range pairs {
					least, _ := strconv.ParseFloat(pair[2], 64)
					if got := contrastOf(t, tokens[pair[0]], tokens[pair[1]]); got < least {
						t.Errorf("accent %s second %s, %s: %s on %s is %.2f:1, want at least %s:1", accent, second, selector, pair[0], pair[1], got, pair[2])
					}
				}
				if tokens["accent"] != accent || tokens["second"] != second {
					t.Errorf("%s: accent %s second %s, want the configured %s and %s", selector, tokens["accent"], tokens["second"], accent, second)
				}
			}
		}
	}
}

func TestTheConfiguredModeIsTheHallsDefault(t *testing.T) {
	for mode, first := range map[string]string{"dark": "dark", "light": "light", "auto": "dark"} {
		css := hallThemeCSS(config.HallTheme{Mode: mode, Accent: "#39FF14", Second: "#5B21B6"})
		if tokens := cssTokens(t, css, `:root, :root[data-theme="`+first+`"]`); tokens["mode"] != first {
			t.Errorf("mode %s: default block says mode %q, want %q", mode, tokens["mode"], first)
		}
		followsSystem := strings.Contains(css, "@media (prefers-color-scheme: light)")
		if followsSystem != (mode == "auto") {
			t.Errorf("mode %s: follows the system's light setting = %v", mode, followsSystem)
		}
	}
	css := hallThemeCSS(config.HallTheme{Mode: "auto", Accent: "#39FF14", Second: "#5B21B6"})
	if tokens := cssTokens(t, css, `  :root:not([data-theme])`); tokens["mode"] != "light" {
		t.Errorf("auto in a light system: mode %q", tokens["mode"])
	}
}

func TestTheHallServesTheUsersThemeAsAStylesheet(t *testing.T) {
	h := readyToRead(t)
	userConfig(t, h, "[hall.theme]\nmode = \"light\"\naccent = \"#22d3ee\"\n")
	hall := startHall(t, h)
	response := hallGet(t, "GET", hall.url+"theme.css", "")
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/css; charset=utf-8" {
		t.Fatalf("theme.css: %s %q", response.Status, response.Header.Get("Content-Type"))
	}
	tokens := cssTokens(t, string(body), `:root, :root[data-theme="light"]`)
	if tokens["mode"] != "light" || tokens["accent"] != "#22D3EE" || tokens["second"] != "#5B21B6" {
		t.Errorf("default block %v", tokens)
	}
	page, _ := io.ReadAll(hallGet(t, "GET", hall.url, "").Body)
	theme, style := strings.Index(string(page), `href="theme.css"`), strings.Index(string(page), `href="hall.css"`)
	if theme < 0 || style < 0 || theme > style {
		t.Errorf("the page does not load theme.css before hall.css:\n%s", page)
	}
}
