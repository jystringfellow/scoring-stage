package cli

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/gear"
	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func putReady(t *testing.T) (*harness, string) {
	t.Helper()
	h, user := carrying(t, `[]`)
	h.expect(0, "up", h.remote.Checkout, "fix")
	h.sbx.SetSandboxes("stage-fix\n")
	return h, user
}

func TestPutCopiesAFileByteForByteAsTheAgentWithoutItsContentOnACommandLine(t *testing.T) {
	h, user := putReady(t)
	body := bytes.Repeat([]byte{0, 255, 27, '\n', 'q'}, 300000)
	from := testutil.WriteFile(t, filepath.Join(user, "file.bin"), string(body))
	h.expect(0, "put", "fix", from, "~/copy.bin")
	if got := h.received("copy.bin")["copy.bin"]; got != string(body) {
		t.Fatal("file content changed")
	}
	f, err := os.Open(h.sbx.BoxFile("copy.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	header, err := tar.NewReader(f).Next()
	if err != nil {
		t.Fatal(err)
	}
	if header.Mode != 0600 || header.Uid != 0 || header.Uname != "" {
		t.Fatalf("archive header: %+v", header)
	}
	call := h.sbx.LastCall()
	if !strings.HasPrefix(call, "exec -i -u agent stage-fix ") || !strings.Contains(call, "tar -xf - --no-overwrite-dir") || !strings.HasSuffix(call, " sh ~/copy.bin") {
		t.Fatal(call)
	}
	if strings.Contains(call, "qqqq") || !strings.Contains(h.stdout.String(), "1500000 bytes) into stage-fix at ~/copy.bin") {
		t.Fatal(h.stdout.String())
	}
}

func TestPutDefaultsToTheSandboxUsersHomeAndReplacesTheFile(t *testing.T) {
	h, user := putReady(t)
	from := testutil.WriteFile(t, filepath.Join(user, "notes"), "first")
	for _, body := range []string{"first", "second"} {
		testutil.WriteFile(t, from, body)
		h.expect(0, "put", "fix", from)
		if h.received("notes")["notes"] != body || !strings.Contains(h.stdout.String(), "at ~/notes") {
			t.Fatal(h.stdout.String())
		}
	}
}

func TestPutRefusesUnsafeSourcesAndDestinationsBeforeExec(t *testing.T) {
	for _, which := range []string{"directory", "missing", "fifo", "stage", "credential", "managed", "large", "relative", "parent", "trailing", "doubled", "control", "root", "login", "workspace", "inside", "holds", "unknown", "absent"} {
		t.Run(which, func(t *testing.T) {
			h, user := putReady(t)
			from := testutil.WriteFile(t, filepath.Join(user, "notes"), "hello")
			to, slug, reason := "~/notes", "fix", "put:"
			switch which {
			case "directory":
				from = filepath.Join(user, "directory")
				if err := os.Mkdir(from, 0700); err != nil {
					t.Fatal(err)
				}
				reason = "regular file"
			case "missing":
				from += "-absent"
				reason = "no such file"
			case "fifo":
				from += "-fifo"
				if err := syscall.Mkfifo(from, 0600); err != nil {
					t.Fatal(err)
				}
				reason = "regular file"
			case "stage":
				from = testutil.WriteFile(t, filepath.Join(h.home, "notes"), "x")
				reason = "stage's own directory"
			case "credential":
				from = testutil.WriteFile(t, filepath.Join(user, ".netrc"), "dummy")
				reason = "credential"
			case "managed":
				testutil.WriteFile(t, h.managed, "allow_gear = false\n")
				reason = "refused by"
			case "large":
				old := gearLimits
				gearLimits = gear.Limits{Bytes: 1, Entries: 10}
				t.Cleanup(func() { gearLimits = old })
				reason = "larger than"
			case "relative":
				to = "notes"
			case "parent":
				to = "~/../notes"
			case "trailing":
				to = "~/notes/"
			case "doubled":
				to = "~/a//b"
			case "control":
				to = "~/\x1bnotes"
			case "root":
				to = "/"
			case "login":
				to = "~/.gemini/antigravity-cli/antigravity-oauth-token"
				reason = "login files"
			case "workspace":
				to = filepath.Join(h.task("fix"), "src")
				reason = "workspace"
			case "inside":
				to = filepath.Join(h.task("fix"), "src", "notes")
				reason = "workspace"
			case "holds":
				to = h.task("fix")
				reason = "workspace"
			case "unknown":
				slug = "unknown"
				reason = "no task"
			case "absent":
				h.sbx.SetSandboxes("")
				reason = "stage down"
			}
			before := len(h.sbx.Calls())
			h.expect(1, "put", slug, from, to)
			h.expectStderr(reason)
			if strings.Contains(h.stderr.String(), "\x1b") {
				t.Fatal("raw escape printed")
			}
			for _, call := range h.sbx.Calls()[before:] {
				if strings.HasPrefix(call, "exec") {
					t.Fatal(call)
				}
			}
		})
	}
}

func TestPutFollowsAnExplicitSourceLinkAndReportsCopyFailures(t *testing.T) {
	h, user := putReady(t)
	from := testutil.WriteFile(t, filepath.Join(user, "notes"), "text")
	link := filepath.Join(user, "linked")
	if err := os.Symlink(from, link); err != nil {
		t.Fatal(err)
	}
	h.expect(0, "put", "fix", link)
	if h.received("linked")["linked"] != "text" {
		t.Fatal("link target missing")
	}
	h.sbx.IgnoreFeeds()
	h.expect(1, "put", "fix", from)
	h.expectStderr("without reading all")
}

func TestPutUsageRequiresAFile(t *testing.T) {
	h := setup(t)
	h.expect(2, "put", "fix")
}

func TestPutEscapesARefusedFileNameAndCannotCopyThroughACredentialLink(t *testing.T) {
	h, user := putReady(t)
	from := testutil.WriteFile(t, filepath.Join(user, "file\x1b"), "x")
	h.expect(1, "put", "fix", from)
	if strings.Contains(h.stderr.String(), "\x1b") || !strings.Contains(h.stderr.String(), `\x1b`) {
		t.Fatal(h.stderr.String())
	}
	credential := testutil.WriteFile(t, filepath.Join(user, ".netrc"), "dummy")
	link := filepath.Join(user, "linked")
	if err := os.Symlink(credential, link); err != nil {
		t.Fatal(err)
	}
	before := len(h.sbx.Calls())
	h.expect(1, "put", "fix", link)
	h.expectStderr("credential")
	for _, call := range h.sbx.Calls()[before:] {
		if strings.HasPrefix(call, "exec") {
			t.Fatal(call)
		}
	}
}
