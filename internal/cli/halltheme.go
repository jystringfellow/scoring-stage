package cli

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jystringfellow/scoring-stage/internal/config"
)

type rgb [3]float64

type themeToken struct{ name, value string }

const readableContrast = 4.6

var (
	darkGrounds = []themeToken{
		{"bg", "#0A0A0A"}, {"panel", "#141414"}, {"inset", "#0F0F0F"}, {"line", "#262626"}, {"line2", "#3A3A3A"},
		{"text", "#F5F5F5"}, {"muted", "#A3A3A3"}, {"idle", "#A3A3A3"}, {"bad", "#FF6B6B"},
		{"term-bg", "#000000"}, {"term-fg", "#D4D4D4"}, {"shadow", "none"},
	}
	lightGrounds = []themeToken{
		{"bg", "#F6F6F4"}, {"panel", "#FFFFFF"}, {"inset", "#F2F2EF"}, {"line", "#E4E4E0"}, {"line2", "#CFCFC9"},
		{"text", "#0A0A0A"}, {"muted", "#525252"}, {"idle", "#5C5C5C"}, {"bad", "#B42318"},
		{"term-bg", "#0A0A0A"}, {"term-fg", "#E5E5E5"}, {"shadow", "0 1px 2px rgba(0, 0, 0, 0.06)"},
	}
)

func hallThemeCSS(theme config.HallTheme) string {
	dark := themeTokens("dark", theme.Accent, theme.Second)
	light := themeTokens("light", theme.Accent, theme.Second)
	first, other := dark, light
	firstMode, otherMode := "dark", "light"
	if theme.Mode == "light" {
		first, other = light, dark
		firstMode, otherMode = "light", "dark"
	}
	var css strings.Builder
	writeThemeBlock(&css, `:root, :root[data-theme="`+firstMode+`"]`, first)
	writeThemeBlock(&css, `:root[data-theme="`+otherMode+`"]`, other)
	if theme.Mode == "auto" {
		css.WriteString("@media (prefers-color-scheme: light) {\n")
		writeThemeBlock(&css, `  :root:not([data-theme])`, light)
		css.WriteString("}\n")
	}
	return css.String()
}

func writeThemeBlock(css *strings.Builder, selector string, tokens []themeToken) {
	css.WriteString(selector + " {\n")
	for _, token := range tokens {
		fmt.Fprintf(css, "  --%s:%s;\n", token.name, token.value)
		if token.name == "mode" {
			fmt.Fprintf(css, "  color-scheme:%s;\n", token.value)
		}
	}
	css.WriteString(strings.Repeat(" ", len(selector)-len(strings.TrimLeft(selector, " "))) + "}\n")
}

func themeTokens(mode, accent, second string) []themeToken {
	grounds := darkGrounds
	toward := rgb{255, 255, 255}
	softness := 0.14
	if mode == "light" {
		grounds = lightGrounds
		toward = rgb{0, 0, 0}
		softness = 0.2
	}
	var surfaces []rgb
	for _, token := range grounds {
		if token.name == "bg" || token.name == "panel" || token.name == "inset" {
			surfaces = append(surfaces, parseColor(token.value))
		}
	}
	tokens := append([]themeToken{{"mode", mode}}, grounds...)
	for _, part := range []struct{ name, color string }{{"accent", accent}, {"second", second}} {
		color := parseColor(part.color)
		tokens = append(tokens,
			themeToken{part.name, part.color},
			themeToken{"on-" + part.name, readableOn(color)},
			themeToken{part.name + "-ink", formatColor(inkFor(color, toward, surfaces))},
			themeToken{part.name + "-soft", fmt.Sprintf("rgba(%d, %d, %d, %g)", int(color[0]), int(color[1]), int(color[2]), softness)},
		)
	}
	return tokens
}

func inkFor(color, toward rgb, surfaces []rgb) rgb {
	for step := 0; step <= 20; step++ {
		candidate := mixColors(color, toward, float64(step)/20)
		readable := true
		for _, surface := range surfaces {
			readable = readable && contrastRatio(candidate, surface) >= readableContrast
		}
		if readable {
			return candidate
		}
	}
	return toward
}

func readableOn(color rgb) string {
	if contrastRatio(color, rgb{0, 0, 0}) >= contrastRatio(color, rgb{255, 255, 255}) {
		return "#000000"
	}
	return "#FFFFFF"
}

func parseColor(hex string) rgb {
	var color rgb
	for i := range color {
		channel, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		color[i] = float64(channel)
	}
	return color
}

func formatColor(color rgb) string {
	return fmt.Sprintf("#%02X%02X%02X", int(math.Round(color[0])), int(math.Round(color[1])), int(math.Round(color[2])))
}

func mixColors(from, to rgb, amount float64) rgb {
	var mixed rgb
	for i := range mixed {
		mixed[i] = math.Round(from[i] + (to[i]-from[i])*amount)
	}
	return mixed
}

func luminance(color rgb) float64 {
	var total float64
	for i, weight := range []float64{0.2126, 0.7152, 0.0722} {
		value := color[i] / 255
		if value <= 0.03928 {
			value /= 12.92
		} else {
			value = math.Pow((value+0.055)/1.055, 2.4)
		}
		total += weight * value
	}
	return total
}

func contrastRatio(a, b rgb) float64 {
	x, y := luminance(a), luminance(b)
	return (math.Max(x, y) + 0.05) / (math.Min(x, y) + 0.05)
}
