package bookref

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func errorf(format string, args ...any) error { return &Error{fmt.Sprintf(format, args...)} }

const host = "github.com"

type Ref struct {
	Owner   string
	Repo    string
	Path    string
	Version string
}

var (
	ownerPattern   = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)
	repoPattern    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,99}$`)
	segmentPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)
	tagPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)
	fullCommit     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	shortCommit    = regexp.MustCompile(`^[0-9a-f]{7,39}$`)
)

func IsRef(text string) bool {
	for _, local := range []string{"./", "../", "~/", "/"} {
		if strings.HasPrefix(text, local) {
			return false
		}
	}
	return strings.Contains(text, "//")
}

func Parse(text string) (Ref, error) {
	quoted := strconv.QuoteToASCII(text)
	form := "write it as github.com/owner/repository//directory@version"
	source, rest, found := strings.Cut(text, "//")
	place, repository, _ := strings.Cut(source, "/")
	if place != host {
		return Ref{}, errorf("book reference %s: only github.com references are supported", quoted)
	}
	if !found {
		return Ref{}, errorf("book reference %s names no book's directory; %s", quoted, form)
	}
	at := strings.LastIndex(rest, "@")
	if at < 0 || at == len(rest)-1 {
		return Ref{}, errorf("book reference %s needs a version: a tag or a full commit; %s", quoted, form)
	}
	ref := Ref{Path: rest[:at], Version: rest[at+1:]}
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || !ownerPattern.MatchString(parts[0]) || !repoPattern.MatchString(parts[1]) {
		return Ref{}, errorf("book reference %s: the repository should be github.com/owner/repository", quoted)
	}
	ref.Owner, ref.Repo = parts[0], parts[1]
	for _, segment := range strings.Split(ref.Path, "/") {
		if !segmentPattern.MatchString(segment) {
			return Ref{}, errorf("book reference %s: %s is not a book's directory; use letters, digits, '.', '_' and '-' between single slashes, not starting with '.'", quoted, strconv.QuoteToASCII(ref.Path))
		}
	}
	switch {
	case fullCommit.MatchString(ref.Version):
	case shortCommit.MatchString(ref.Version):
		return Ref{}, errorf("book reference %s: a commit must be the full 40-character commit", quoted)
	case !tagPattern.MatchString(ref.Version) || strings.Contains(ref.Version, "..") || strings.HasSuffix(ref.Version, ".lock"):
		return Ref{}, errorf("book reference %s: %s is not a tag or a full commit", quoted, strconv.QuoteToASCII(ref.Version))
	}
	return ref, nil
}

func (r Ref) String() string {
	return host + "/" + r.Owner + "/" + r.Repo + "//" + r.Path + "@" + r.Version
}

func (r Ref) URL() string { return "https://" + host + "/" + r.Owner + "/" + r.Repo + ".git" }

func (r Ref) repository() string { return host + "/" + r.Owner + "/" + r.Repo }
