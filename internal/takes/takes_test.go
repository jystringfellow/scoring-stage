package takes

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type entry struct {
	name, body, link string
	kind             byte
}

func archive(t *testing.T, entries ...entry) *bytes.Reader {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, e := range entries {
		kind := e.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{Name: e.name, Typeflag: kind, Linkname: e.link, Mode: 0o777, Size: int64(len(e.body))}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buffer.Bytes())
}

func TestFilesAndDirectoriesAreWrittenPrivateUnderTheDirectory(t *testing.T) {
	dir := t.TempDir()
	summary, err := Extract(archive(t,
		entry{name: "home/agent/runs/", kind: tar.TypeDir},
		entry{name: "home/agent/runs/one/result.json", body: "{}"},
		entry{name: "./home/agent/runs/two.txt", body: "second"},
	), dir, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if summary != (Summary{Files: 2, Bytes: 8}) {
		t.Errorf("summary = %+v", summary)
	}
	file := filepath.Join(dir, "home", "agent", "runs", "one", "result.json")
	if content, err := os.ReadFile(file); err != nil || string(content) != "{}" {
		t.Fatalf("content %q, %v", content, err)
	}
	for path, mode := range map[string]os.FileMode{file: 0o600, filepath.Dir(file): 0o700, filepath.Join(dir, "home"): 0o700} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != mode {
			t.Errorf("%s mode %v, want %v", path, info.Mode().Perm(), mode)
		}
	}
}

func TestAnEntryThatWouldLandOutsideTheDirectoryIsRefused(t *testing.T) {
	for _, name := range []string{"../escape.txt", "/escape.txt", "home/../../escape.txt", ".."} {
		parent := t.TempDir()
		dir := filepath.Join(parent, "take")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := Extract(archive(t, entry{name: "ok.txt", body: "fine"}, entry{name: name, body: "out"}), dir, DefaultLimits)
		if err == nil || !strings.Contains(err.Error(), "outside the take") {
			t.Errorf("%q: error %v", name, err)
		}
		if entries, _ := os.ReadDir(parent); len(entries) != 1 {
			t.Errorf("%q: wrote outside the take: %v", name, entries)
		}
	}
}

func TestLinksAndSpecialFilesAreLeftBehindAndCounted(t *testing.T) {
	dir := t.TempDir()
	summary, err := Extract(archive(t,
		entry{name: "passwd", kind: tar.TypeSymlink, link: "/etc/passwd"},
		entry{name: "hard", kind: tar.TypeLink, link: "kept.txt"},
		entry{name: "pipe", kind: tar.TypeFifo},
		entry{name: "kept.txt", body: "kept"},
	), dir, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if summary != (Summary{Files: 1, Bytes: 4, Skipped: 3}) {
		t.Errorf("summary = %+v", summary)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 || entries[0].Name() != "kept.txt" {
		t.Errorf("entries = %v", entries)
	}
}

func TestATakeOverItsSizeOrEntryLimitIsRefused(t *testing.T) {
	two := []entry{{name: "a", body: "12345"}, {name: "b", body: "67890"}}
	if _, err := Extract(archive(t, two...), t.TempDir(), Limits{Bytes: 9, Entries: 10}); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("size: error %v", err)
	}
	if _, err := Extract(archive(t, two...), t.TempDir(), Limits{Bytes: 100, Entries: 1}); err == nil || !strings.Contains(err.Error(), "more than 1 entries") {
		t.Errorf("entries: error %v", err)
	}
}

func TestAnArchiveThatNamesAFileTwiceIsRefused(t *testing.T) {
	_, err := Extract(archive(t, entry{name: "a", body: "first"}, entry{name: "a", body: "second"}), t.TempDir(), DefaultLimits)
	if err == nil {
		t.Fatal("a second copy replaced the first")
	}
}

func TestATruncatedArchiveIsAnError(t *testing.T) {
	whole := archive(t, entry{name: "a", body: strings.Repeat("x", 4096)})
	cut := make([]byte, 1024)
	if _, err := whole.Read(cut); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(bytes.NewReader(cut), t.TempDir(), DefaultLimits); err == nil {
		t.Fatal("kept a truncated take without an error")
	}
}
