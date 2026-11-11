package config

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f || r >= 0x200b && r <= 0x200f ||
		r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 || r == 0xfeff
}

func isHidden(r rune) bool {
	return isControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) || r == 0x115f || r == 0x1160 || r == 0x3164
}

func HasHidden(text string) bool {
	return !utf8.ValidString(text) || strings.IndexFunc(text, isHidden) >= 0
}

func Printable(text string) string {
	var out strings.Builder
	for _, r := range text {
		if isControl(r) {
			fmt.Fprintf(&out, "\\x%02x", r)
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func HasControl(text string) bool {
	return strings.IndexFunc(text, isControl) >= 0
}
