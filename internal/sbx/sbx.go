package sbx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type Version [3]int

var MinVersion = Version{0, 51, 0}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

func (v Version) Less(other Version) bool {
	for i := range v {
		if v[i] != other[i] {
			return v[i] < other[i]
		}
	}
	return false
}

type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func Run(args ...string) error {
	_, err := run(false, os.Stdin, args)
	return err
}

func Output(args ...string) (string, error) {
	return run(true, os.Stdin, args)
}

// Feed runs sbx with stdin from input and its output captured.
func Feed(input io.Reader, args ...string) error {
	_, err := run(true, input, args)
	return err
}

func run(capture bool, stdin io.Reader, args []string) (string, error) {
	command := exec.Command("sbx", args...)
	command.Stdin = stdin
	var stdout, stderr bytes.Buffer
	if capture {
		command.Stdout, command.Stderr = &stdout, &stderr
	} else {
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
	}
	err := command.Run()
	if errors.Is(err, exec.ErrNotFound) {
		return "", &Error{"sbx is not installed or not on PATH"}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		message := "sbx " + strings.Join(args[:min(2, len(args))], " ") + " failed"
		if detail := LastLine(stderr.String()); detail != "" {
			message += ": " + detail
		}
		return "", &Error{message}
	}
	if err != nil {
		return "", &Error{fmt.Sprintf("cannot run sbx: %v", err)}
	}
	return stdout.String(), nil
}

func LastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

var versionPattern = regexp.MustCompile(`\bv?(\d+)\.(\d+)\.(\d+)\b`)

func ParseVersion(text string) (Version, error) {
	match := versionPattern.FindStringSubmatch(text)
	if match == nil {
		return Version{}, &Error{fmt.Sprintf("cannot read an sbx version from %q", strings.TrimSpace(text))}
	}
	var version Version
	for i := range version {
		version[i], _ = strconv.Atoi(match[i+1])
	}
	return version, nil
}

func CheckVersion(found Version) error {
	if found.Less(MinVersion) {
		return &Error{fmt.Sprintf("sbx %s is too old; scoring-stage needs %s or later", found, MinVersion)}
	}
	return nil
}

func RequireVersion() (Version, error) {
	text, err := Output("version")
	if err != nil {
		return Version{}, err
	}
	found, err := ParseVersion(text)
	if err != nil {
		return Version{}, err
	}
	return found, CheckVersion(found)
}

type Policy struct {
	Rules []map[string]any `json:"rules"`
}

func GlobalAllowRules(policy Policy) []string {
	field := func(rule map[string]any, key, fallback string) string {
		if value, ok := rule[key].(string); ok {
			return value
		}
		return fallback
	}
	ids := []string{}
	for _, rule := range policy.Rules {
		resource := field(rule, "resource_type", "")
		if field(rule, "scope", "") == "global" &&
			field(rule, "decision", "") == "allow" &&
			(resource == "network" || resource == "http") &&
			field(rule, "status", "active") == "active" {
			ids = append(ids, field(rule, "id", "?"))
		}
	}
	return ids
}

func ReadPolicy() (Policy, error) {
	var policy Policy
	text, err := Output("policy", "ls", "--json")
	if err != nil {
		return policy, err
	}
	if err := json.Unmarshal([]byte(text), &policy); err != nil {
		return policy, &Error{fmt.Sprintf("cannot read sbx policy ls --json: %v", err)}
	}
	return policy, nil
}

func GlobalAllows() ([]string, error) {
	policy, err := ReadPolicy()
	if err != nil {
		return nil, err
	}
	return GlobalAllowRules(policy), nil
}

func Sandboxes() (map[string]bool, error) {
	text, err := Output("ls", "-q")
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, name := range strings.Fields(text) {
		names[name] = true
	}
	return names, nil
}
