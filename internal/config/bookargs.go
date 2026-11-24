package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

type bookArgs map[string]map[string]string

type bookUse struct {
	text string
	args bookArgs
}

const bookArgsForm = `args = { <kit> = { <argument> = "<value>" } }`

func decodeBookUse(entry any, where string) (bookUse, error) {
	switch typed := entry.(type) {
	case string:
		return bookUse{text: typed}, nil
	case map[string]any:
		source, ok := typed["source"].(string)
		if !ok || len(unknownKeys(typed, []string{"source", "args"})) > 0 {
			break
		}
		args, err := decodeBookArgs(typed["args"], where+": "+Printable(source))
		if err != nil {
			return bookUse{}, err
		}
		return bookUse{text: source, args: args}, nil
	}
	return bookUse{}, errorf("%s: a book is named by a string or {source, args}, not %s", where, Printable(fmt.Sprint(entry)))
}

func decodeBookUses(value any, key, where string) ([]bookUse, error) {
	if value == nil {
		return nil, nil
	}
	list, ok := asList(value)
	if !ok {
		return nil, errorf("%s: %s must be a list", where, key)
	}
	uses := make([]bookUse, 0, len(list))
	for _, entry := range list {
		use, err := decodeBookUse(entry, where+": "+key)
		if err != nil {
			return nil, err
		}
		uses = append(uses, use)
	}
	return uses, nil
}

func decodeBookArgs(value any, where string) (bookArgs, error) {
	if value == nil {
		return nil, nil
	}
	shape := errorf("%s: kit arguments are written %s", where, bookArgsForm)
	table, ok := value.(map[string]any)
	if !ok {
		return nil, shape
	}
	args := bookArgs{}
	for _, kit := range sortedKeys(table) {
		values, ok := table[kit].(map[string]any)
		if !ok {
			return nil, shape
		}
		for _, name := range slices.Sorted(maps.Keys(values)) {
			value, ok := values[name].(string)
			if !ok || HasControl(kit) || HasControl(name) || HasControl(value) {
				return nil, errorf("%s: argument %s.%s must be a string without control characters", where, Printable(kit), Printable(name))
			}
			if args[kit] == nil {
				args[kit] = map[string]string{}
			}
			args[kit][name] = value
		}
	}
	if len(args) == 0 {
		return nil, nil
	}
	return args, nil
}

func (a bookArgs) key() string {
	if len(a) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(a)
	return string(encoded)
}

func (a bookArgs) lines() []string {
	var lines []string
	for _, kit := range slices.Sorted(maps.Keys(a)) {
		for _, name := range slices.Sorted(maps.Keys(a[kit])) {
			lines = append(lines, kit+"."+name+" = "+a[kit][name])
		}
	}
	return lines
}

func (u bookUse) String() string {
	lines := u.args.lines()
	switch len(lines) {
	case 0:
		return u.text
	case 1:
		return u.text + " with argument " + lines[0]
	}
	return u.text + " with arguments " + strings.Join(lines, ", ")
}

func (b Book) withArgs(args bookArgs, place string) (Book, error) {
	return b.addArgs(args, func(string) string { return place })
}

func (b Book) withArgsOf(other Book) (Book, error) {
	return b.addArgs(other.args, func(key string) string { return other.argPlaces[key] })
}

func (b Book) addArgs(args bookArgs, placeOf func(key string) string) (Book, error) {
	if len(args) == 0 {
		return b, nil
	}
	merged, places := bookArgs{}, map[string]string{}
	for kit, values := range b.args {
		merged[kit] = map[string]string{}
		for name, value := range values {
			merged[kit][name] = value
		}
	}
	for key, where := range b.argPlaces {
		places[key] = where
	}
	for _, kit := range slices.Sorted(maps.Keys(args)) {
		for _, name := range slices.Sorted(maps.Keys(args[kit])) {
			value, key := args[kit][name], kit+"."+name
			place := placeOf(key)
			if existing, set := merged[kit][name]; set {
				if existing != value {
					return Book{}, errorf("book %s: argument %s is %q from %s and %q from %s; give it one value",
						b.Name, Printable(key), existing, places[key], value, place)
				}
				continue
			}
			if merged[kit] == nil {
				merged[kit] = map[string]string{}
			}
			merged[kit][name], places[key] = value, place
		}
	}
	b.args, b.argPlaces = merged, places
	return b, nil
}

func (b Book) applyArgs() (Book, error) {
	if len(b.args) == 0 {
		return b, nil
	}
	owner := "book " + b.Name
	sources := map[string]string{}
	var names []string
	rewrite := func(entries []any) ([]any, error) {
		rewritten := make([]any, len(entries))
		for i, entry := range entries {
			rewritten[i] = entry
			kit, err := parseKit(entry, b.Path, owner)
			if err != nil {
				return nil, err
			}
			if !kit.IsLocal() {
				continue
			}
			name := filepath.Base(kit.Source)
			if other, seen := sources[name]; seen && other != kit.Source {
				if _, given := b.args[name]; given {
					return nil, errorf("%s: two of its kits are named %s, so arguments for %s are ambiguous", owner, name, name)
				}
			}
			if _, seen := sources[name]; !seen {
				names = append(names, name)
			}
			sources[name] = kit.Source
			values, given := b.args[name]
			if !given {
				continue
			}
			if err := checkKitArgs(kit.Source, name, values, owner); err != nil {
				return nil, err
			}
			args := map[string]any{}
			for key, value := range kit.Args {
				args[key] = value
			}
			for key, value := range values {
				args[key] = value
			}
			rewritten[i] = map[string]any{"source": kitSource(entry), "args": args}
		}
		return rewritten, nil
	}
	kits, err := rewrite(b.kits)
	if err != nil {
		return Book{}, err
	}
	repos := make([]repo, len(b.repos))
	for i, r := range b.repos {
		if r.kits, err = rewrite(r.kits); err != nil {
			return Book{}, err
		}
		repos[i] = r
	}
	for _, kit := range slices.Sorted(maps.Keys(b.args)) {
		if _, found := sources[kit]; !found {
			sort.Strings(names)
			return Book{}, errorf("%s has no kit named %s (from %s); its kits are %s", owner, Printable(kit), b.argPlaces[kit+"."+slices.Sorted(maps.Keys(b.args[kit]))[0]], strings.Join(names, ", "))
		}
	}
	b.kits, b.repos = kits, repos
	return b, nil
}

type kitArgument struct {
	Pattern string `yaml:"pattern"`
}

func checkKitArgs(dir, name string, values map[string]string, owner string) error {
	documents, err := loadKit(dir)
	if err != nil {
		return err
	}
	declared := map[string]kitArgument{}
	for _, document := range documents {
		for argument, spec := range document.spec.Args {
			declared[argument] = spec
		}
	}
	for _, argument := range slices.Sorted(maps.Keys(values)) {
		value, key := values[argument], name+"."+argument
		spec, found := declared[argument]
		if !found {
			return errorf("%s: kit %s has no argument %s (its arguments: %s)", owner, name, Printable(argument), strings.Join(slices.Sorted(maps.Keys(declared)), ", "))
		}
		if spec.Pattern == "" {
			return errorf("%s: kit argument %s has no pattern, so stage cannot check a value given for it; set it in a copy of the book", owner, key)
		}
		pattern, err := regexp.Compile("^(?:" + spec.Pattern + ")$")
		if err != nil {
			return errorf("%s: kit argument %s has a pattern stage cannot read: %v", owner, key, err)
		}
		if !pattern.MatchString(value) {
			return errorf("%s: %s = %q does not match the kit's pattern %s", owner, key, value, Printable(spec.Pattern))
		}
	}
	return nil
}

func (b Book) hasArgsFrom(place string) bool {
	for _, where := range b.argPlaces {
		if where == place {
			return true
		}
	}
	return false
}

func (b Book) argDetails() []string {
	var details []string
	for _, kit := range slices.Sorted(maps.Keys(b.args)) {
		for _, name := range slices.Sorted(maps.Keys(b.args[kit])) {
			key := kit + "." + name
			details = append(details, fmt.Sprintf("argument %s = %s (from %s)", key, b.args[kit][name], b.argPlaces[key]))
		}
	}
	return details
}

func (b Book) approvalFingerprint(content string) string {
	key := b.args.key()
	if key == "" {
		return content
	}
	hash := sha256.Sum256([]byte("book args\x00" + content + "\x00" + key))
	return hex.EncodeToString(hash[:])
}
