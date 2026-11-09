package source

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jystringfellow/scoring-stage/internal/sbx"
)

var githubURL = regexp.MustCompile(`^(?:git@github\.com:|ssh://git@github\.com/|https://github\.com/)([^/]+/[^/]+?)(?:\.git)?/?$`)

type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func Git(repo string, args ...string) (string, error) {
	return git(true, repo, args)
}

func GitInteractive(repo string, args ...string) error {
	_, err := git(false, repo, args)
	return err
}

// hardened keeps git on the host from running anything a repository's own
// config names, in case a sandbox ever writes to a scratch clone's .git.
var hardened = []string{
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.fsmonitor=false",
	"-c", "core.pager=cat",
	"-c", "protocol.ext.allow=never",
}

func git(capture bool, repo string, args []string) (string, error) {
	command := exec.Command("git", append(append(append([]string{}, hardened...), "-C", repo), args...)...)
	command.Stdin = os.Stdin
	var stdout, stderr bytes.Buffer
	if capture {
		command.Stdout, command.Stderr = &stdout, &stderr
	} else {
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
	}
	if err := command.Run(); err != nil {
		message := fmt.Sprintf("git %s failed in %s", strings.Join(args[:min(2, len(args))], " "), repo)
		if detail := sbx.LastLine(stderr.String()); detail != "" {
			message += ": " + detail
		}
		return "", &Error{message}
	}
	return strings.TrimSpace(stdout.String()), nil
}

func GitHubRepo(url string) (string, error) {
	match := githubURL.FindStringSubmatch(strings.TrimSpace(url))
	if match == nil {
		return "", &Error{"origin is not a github.com repository: " + url}
	}
	return match[1], nil
}

func DefaultBranch(repo, scratch string) (string, error) {
	if ref, err := Git(repo, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil && ref != "" {
		return strings.TrimPrefix(ref, "origin/"), nil
	}
	listing, err := Git(scratch, "ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(listing, "\n") {
		if fields := strings.Fields(line); len(fields) > 1 && strings.HasPrefix(line, "ref:") {
			return strings.TrimPrefix(fields[1], "refs/heads/"), nil
		}
	}
	return "", &Error{"could not determine the default branch; pass --base"}
}

func Prepare(repo, scratch, github, base string) (string, string, error) {
	if _, err := os.Stat(filepath.Join(scratch, ".git")); err != nil {
		var stderr bytes.Buffer
		clone := exec.Command("git", "clone", "--quiet", "--no-hardlinks", repo, scratch)
		clone.Stderr = &stderr
		if err := clone.Run(); err != nil {
			return "", "", &Error{"git clone failed: " + strings.TrimSpace(stderr.String())}
		}
	}
	if _, err := Git(scratch, "remote", "set-url", "origin", "https://github.com/"+github+".git"); err != nil {
		return "", "", err
	}
	if base == "" {
		var err error
		if base, err = DefaultBranch(repo, scratch); err != nil {
			return "", "", err
		}
	}
	if _, err := Git(scratch, "fetch", "--quiet", "origin", base); err != nil {
		return "", "", err
	}
	if _, err := Git(scratch, "checkout", "--quiet", "--detach", "FETCH_HEAD"); err != nil {
		return "", "", err
	}
	head, err := Git(scratch, "rev-parse", "HEAD")
	return base, head, err
}
