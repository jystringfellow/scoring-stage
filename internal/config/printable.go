package config

import (
	"fmt"
	"strings"
)

func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f || r >= 0x200b && r <= 0x200f ||
		r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 || r == 0xfeff
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
