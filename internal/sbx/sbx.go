package sbx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
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
	if err := failure(command.Run(), args, stderr.String()); err != nil {
		return "", err
	}
	return stdout.String(), nil
}

func failure(err error, args []string, stderr string) error {
	if errors.Is(err, exec.ErrNotFound) {
		return &Error{"sbx is not installed or not on PATH"}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		message := "sbx " + strings.Join(args[:min(2, len(args))], " ") + " failed"
		if detail := LastLine(stderr); detail != "" {
			message += ": " + detail
		}
		return &Error{message}
	}
	if err != nil {
		return &Error{fmt.Sprintf("cannot run sbx: %v", err)}
	}
	return nil
}

const stderrKept = 4096

// Stream hands sbx's output to consume as it arrives, and consume must read it
// to the end. sbx is stopped when consume fails or the timeout passes.
func Stream(timeout time.Duration, consume func(io.Reader) error, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, "sbx", args...)
	command.WaitDelay = time.Second
	stderr := &tail{limit: stderrKept}
	command.Stderr = stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return &Error{fmt.Sprintf("cannot run sbx: %v", err)}
	}
	if err := failure(command.Start(), args, ""); err != nil {
		return err
	}
	context.AfterFunc(ctx, func() { stdout.Close() })
	consumeErr := consume(stdout)
	if consumeErr != nil {
		cancel()
	}
	waitErr := command.Wait()
	said := LastLine(string(stderr.kept))
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return &Error{fmt.Sprintf("sbx %s did not finish within %s", strings.Join(args[:min(2, len(args))], " "), timeout)}
	case consumeErr != nil && said != "":
		return fmt.Errorf("%w (sbx said: %s)", consumeErr, said)
	case consumeErr != nil:
		return consumeErr
	}
	return failure(waitErr, args, string(stderr.kept))
}

type tail struct {
	kept  []byte
	limit int
}

func (t *tail) Write(p []byte) (int, error) {
	t.kept = append(t.kept, p...)
	if over := len(t.kept) - t.limit; over > 0 {
		t.kept = append([]byte(nil), t.kept[over:]...)
	}
	return len(p), nil
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

// GlobalSecrets lists the services that have a global sbx secret. Any binding
// for such a service, saved by an earlier sandbox or not, can fall back to it.
func GlobalSecrets() ([]string, error) {
	text, err := Output("secret", "ls", "--global", "--json")
	if err != nil {
		return nil, err
	}
	var listing struct {
		Secrets *[]struct {
			Scope string `json:"scope"`
			Type  string `json:"type"`
			Name  string `json:"name"`
		} `json:"secrets"`
	}
	if err := json.Unmarshal([]byte(text), &listing); err != nil || listing.Secrets == nil {
		return nil, &Error{fmt.Sprintf("cannot read sbx secret ls --json (expected a secrets list): %v", err)}
	}
	var services []string
	for _, secret := range *listing.Secrets {
		if secret.Scope == "" || secret.Type == "" || secret.Name == "" {
			return nil, &Error{"cannot read sbx secret ls --json: a secret has no scope, type or name"}
		}
		if secret.Scope == "global" && secret.Type == "service" {
			services = append(services, secret.Name)
		}
	}
	return services, nil
}
