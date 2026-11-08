package yamlout

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Map is a mapping written in its own order; a map[string]any is written with sorted keys.
type Map []Item

type Item struct {
	Key   string
	Value any
}

var plainKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

var reserved = map[string]bool{"true": true, "false": true, "null": true, "yes": true, "no": true, "on": true, "off": true}

func Dump(value any) (string, error) {
	lines, err := block(value, 0)
	if err != nil {
		return "", err
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func block(value any, level int) ([]string, error) {
	pad := strings.Repeat("  ", level)
	var lines []string
	if items, ok := mapping(value); ok {
		for _, item := range items {
			if nonEmptyCollection(item.Value) {
				lines = append(lines, pad+key(item.Key)+":")
				nested, err := block(item.Value, level+1)
				if err != nil {
					return nil, err
				}
				lines = append(lines, nested...)
				continue
			}
			text, err := inline(item.Value)
			if err != nil {
				return nil, err
			}
			lines = append(lines, pad+key(item.Key)+": "+text)
		}
		return lines, nil
	}
	if elements, ok := list(value); ok {
		for _, element := range elements {
			if items, isMap := mapping(element); isMap && len(items) > 0 {
				nested, err := block(element, level+1)
				if err != nil {
					return nil, err
				}
				lines = append(lines, pad+"- "+strings.TrimLeft(nested[0], " "))
				lines = append(lines, nested[1:]...)
				continue
			}
			if inner, isList := list(element); isList && len(inner) > 0 {
				nested, err := block(element, level+1)
				if err != nil {
					return nil, err
				}
				lines = append(lines, pad+"-")
				lines = append(lines, nested...)
				continue
			}
			text, err := inline(element)
			if err != nil {
				return nil, err
			}
			lines = append(lines, pad+"- "+text)
		}
		return lines, nil
	}
	return nil, fmt.Errorf("top-level value must be a mapping or list, not %T", value)
}

func mapping(value any) (Map, bool) {
	switch typed := value.(type) {
	case Map:
		return typed, true
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for k := range typed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		items := make(Map, 0, len(keys))
		for _, k := range keys {
			items = append(items, Item{k, typed[k]})
		}
		return items, true
	}
	return nil, false
}

func list(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []string:
		elements := make([]any, len(typed))
		for i, s := range typed {
			elements[i] = s
		}
		return elements, true
	case []map[string]any:
		elements := make([]any, len(typed))
		for i, m := range typed {
			elements[i] = m
		}
		return elements, true
	}
	return nil, false
}

func nonEmptyCollection(value any) bool {
	if items, ok := mapping(value); ok {
		return len(items) > 0
	}
	if elements, ok := list(value); ok {
		return len(elements) > 0
	}
	return false
}

func key(k string) string {
	if plainKey.MatchString(k) && !reserved[k] {
		return k
	}
	return Quote(k)
}

func inline(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "null", nil
	case bool:
		return strconv.FormatBool(typed), nil
	case int:
		return strconv.Itoa(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case string:
		return Quote(typed), nil
	}
	if _, ok := mapping(value); ok {
		return "{}", nil
	}
	if _, ok := list(value); ok {
		return "[]", nil
	}
	return "", fmt.Errorf("cannot write %T as YAML", value)
}

// Quote writes s as an ASCII-only double-quoted scalar, the same text JSON would use.
func Quote(s string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		default:
			switch {
			case r < 0x20 || r == 0x7f:
				fmt.Fprintf(&out, `\u%04x`, r)
			case r < utf8.RuneSelf:
				out.WriteRune(r)
			case r > 0xffff:
				r -= 0x10000
				fmt.Fprintf(&out, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			default:
				fmt.Fprintf(&out, `\u%04x`, r)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}
