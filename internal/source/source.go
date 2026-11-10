package source

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
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
		absolute, err := filepath.Abs(scratch)
		if err != nil {
			return "", "", err
		}
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			return "", "", err
		}
		origin := repo
		if _, err := os.Stat(repo); err == nil {
			if origin, err = filepath.Abs(repo); err != nil {
				return "", "", err
			}
		}
		if _, err := Git(filepath.Dir(absolute), "clone", "--quiet", "--no-hardlinks", "--", origin, absolute); err != nil {
			return "", "", err
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

// ConfigFingerprint hashes the parts of a clone's .git that tell git what to
// run or where to connect, so ship can refuse a clone whose config changed.
func ConfigFingerprint(repo, sandbox string) (string, error) {
	gitDir := filepath.Join(repo, ".git")
	hash := sha256.New()
	var files []string
	for _, name := range []string{"config", "config.worktree", "commondir"} {
		files = append(files, filepath.Join(gitDir, name))
	}
	err := filepath.WalkDir(filepath.Join(gitDir, "info"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	sort.Strings(files[3:])
	for _, path := range files {
		relative, _ := filepath.Rel(gitDir, path)
		content, err := os.ReadFile(path)
		switch {
		case os.IsNotExist(err):
			fmt.Fprintf(hash, "%s\x00absent\x00", relative)
		case err != nil:
			return "", err
		default:
			if relative == "config" {
				content = withoutSbxRemote(content, sandbox)
			}
			fmt.Fprintf(hash, "%s\x00%d\x00", relative, len(content))
			hash.Write(content)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

var sbxRemoteURL = regexp.MustCompile(`^\turl = git://127\.0\.0\.1:[0-9]{1,5}/src$`)

func withoutSbxRemote(config []byte, sandbox string) []byte {
	lines := strings.SplitAfter(string(config), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	header := "[remote \"sandbox-" + sandbox + "\"]\n"
	want := []string{
		"\tfetch = +refs/heads/*:refs/remotes/sandbox-" + sandbox + "/*\n",
		"\tfetch = +refs/heads/*:refs/sandboxes/" + sandbox + "/*\n",
	}
	for start, line := range lines {
		if line != header {
			continue
		}
		end := start + 1
		for end < len(lines) && !strings.HasPrefix(lines[end], "[") {
			end++
		}
		body := lines[start+1 : end]
		if len(body) == 3 && sbxRemoteURL.MatchString(strings.TrimSuffix(body[0], "\n")) && body[1] == want[0] && body[2] == want[1] {
			return []byte(strings.Join(append(lines[:start:start], lines[end:]...), ""))
		}
		return config
	}
	return config
}
