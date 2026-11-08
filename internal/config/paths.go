package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func ExpandUser(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

// Resolve makes path absolute and follows symlinks through its longest existing prefix.
func Resolve(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	existing, rest := absolute, ""
	for {
		if resolved, err := filepath.EvalSymlinks(existing); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return absolute
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// Fnmatch matches name against a shell-style pattern in which * and ? also match "/".
func Fnmatch(name, pattern string) bool {
	var expr strings.Builder
	expr.WriteString(`(?s)^`)
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			expr.WriteString(`.*`)
		case '?':
			expr.WriteString(`.`)
		case '[':
			end := i + 1
			if end < len(pattern) && pattern[end] == '!' {
				end++
			}
			if end < len(pattern) && pattern[end] == ']' {
				end++
			}
			for end < len(pattern) && pattern[end] != ']' {
				end++
			}
			if end >= len(pattern) {
				expr.WriteString(`\[`)
				continue
			}
			class := pattern[i+1 : end]
			negate := strings.HasPrefix(class, "!")
			class = strings.TrimPrefix(class, "!")
			class = strings.ReplaceAll(class, `\`, `\\`)
			class = strings.ReplaceAll(class, `[`, `\[`)
			if negate {
				expr.WriteString("[^" + class + "]")
			} else if strings.HasPrefix(class, "^") {
				expr.WriteString(`[\` + class + "]")
			} else {
				expr.WriteString("[" + class + "]")
			}
			i = end
		default:
			expr.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	expr.WriteString(`$`)
	matcher, err := regexp.Compile(expr.String())
	return err == nil && matcher.MatchString(name)
}
