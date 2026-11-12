package gear

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func item(hostPath, to string) config.Gear {
	return config.Gear{From: hostPath, To: to, HostPath: hostPath}
}

func write(t *testing.T, path, content string, mode os.FileMode) string {
	t.Helper()
	testutil.WriteFile(t, path, content)
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

type archived struct {
	Name string
	Mode int64
	Body string
	Dir  bool
}

func archive(t *testing.T, source Source) []archived {
	t.Helper()
	var buffer bytes.Buffer
	if err := source.Archive(&buffer); err != nil {
		t.Fatal(err)
	}
	var entries []archived
	reader := tar.NewReader(&buffer)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if header.Uid != 0 || header.Gid != 0 || header.Uname != "" || header.Gname != "" {
			t.Errorf("%s carries an owner: %+v", header.Name, header)
		}
		entries = append(entries, archived{Name: header.Name, Mode: header.Mode, Body: string(body), Dir: header.Typeflag == tar.TypeDir})
	}
}

func TestAFileIsArchivedUnderItsDestinationsNameWithOnlyItsOwnersPermissions(t *testing.T) {
	dir := t.TempDir()
	settingsFile := write(t, filepath.Join(dir, "config.toml"), "theme = \"dark\"\n", 0o644)
	script := write(t, filepath.Join(dir, "hook.sh"), "#!/bin/sh\n", 0o755)

	survey, err := Look([]config.Gear{item(settingsFile, "~/.tool/settings.toml"), item(script, "/srv/hooks/run")}, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}

	if survey.Files != 2 || survey.Bytes != 25 || survey.Skipped != 0 || len(survey.Missing) != 0 || len(survey.Sources) != 2 {
		t.Fatalf("survey = %+v", survey)
	}
	if got, want := archive(t, survey.Sources[0]), []archived{{Name: "settings.toml", Mode: 0o600, Body: "theme = \"dark\"\n"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("archive = %+v\nwant %+v", got, want)
	}
	if got, want := archive(t, survey.Sources[1]), []archived{{Name: "run", Mode: 0o700, Body: "#!/bin/sh\n"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("archive = %+v\nwant %+v", got, want)
	}
}

func TestADirectoryIsArchivedWholeUnderItsDestinationsName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "presets")
	write(t, filepath.Join(dir, "a.json"), "{}", 0o640)
	write(t, filepath.Join(dir, "deep", "b.json"), "[]", 0o600)
	if err := os.Chmod(filepath.Join(dir, "deep"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(dir, "deep"), 0o755) })

	survey, err := Look([]config.Gear{item(dir, "~/.tool/mine")}, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}

	want := []archived{
		{Name: "mine/", Mode: 0o700, Dir: true},
		{Name: "mine/a.json", Mode: 0o600, Body: "{}"},
		{Name: "mine/deep/", Mode: 0o700, Dir: true},
		{Name: "mine/deep/b.json", Mode: 0o600, Body: "[]"},
	}
	if got := archive(t, survey.Sources[0]); !reflect.DeepEqual(got, want) {
		t.Errorf("archive = %+v\nwant %+v", got, want)
	}
	if survey.Files != 2 || survey.Bytes != 4 {
		t.Errorf("survey = %+v", survey)
	}
}

func TestLinksAndSpecialFilesInsideADirectoryAreLeftOutAndCounted(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "presets")
	outside := write(t, filepath.Join(root, "outside", "secret"), "do not carry", 0o600)
	write(t, filepath.Join(dir, "kept.json"), "{}", 0o600)
	for name, target := range map[string]string{"file-link": outside, "dir-link": filepath.Dir(outside), "inner-link": "kept.json", "dangling": "nowhere"} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}

	survey, err := Look([]config.Gear{item(dir, "~/presets")}, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}

	if survey.Skipped != 5 || survey.Files != 1 {
		t.Errorf("survey = %+v", survey)
	}
	for _, entry := range archive(t, survey.Sources[0]) {
		if entry.Name != "presets/" && entry.Name != "presets/kept.json" {
			t.Errorf("archived %q", entry.Name)
		}
		if strings.Contains(entry.Body, "do not carry") {
			t.Errorf("%s carries a file from outside the directory", entry.Name)
		}
	}
}

func TestASourceThatIsNotThereIsPassedOverAndNamed(t *testing.T) {
	root := t.TempDir()
	present := write(t, filepath.Join(root, "here"), "x", 0o600)
	dangling := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "gone"), dangling); err != nil {
		t.Fatal(err)
	}

	survey, err := Look([]config.Gear{item(filepath.Join(root, "absent"), "~/absent"), item(present, "~/here"), item(dangling, "~/dangling")}, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}

	if len(survey.Sources) != 1 || survey.Sources[0].To != "~/here" {
		t.Errorf("sources = %+v", survey.Sources)
	}
	if len(survey.Missing) != 2 || survey.Missing[0].To != "~/absent" || survey.Missing[1].To != "~/dangling" {
		t.Errorf("missing = %+v", survey.Missing)
	}
}

func TestASourceThatIsNeitherAFileNorADirectoryIsAnError(t *testing.T) {
	pipe := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Look([]config.Gear{item(pipe, "~/pipe")}, DefaultLimits)
	if err == nil || !strings.Contains(err.Error(), "is neither a file nor a directory") {
		t.Fatalf("error %v", err)
	}
}

func TestGearOverTheByteOrEntryLimitIsAnErrorAcrossAllOfIt(t *testing.T) {
	root := t.TempDir()
	one := write(t, filepath.Join(root, "one"), "12345", 0o600)
	two := write(t, filepath.Join(root, "two"), "678901", 0o600)
	dir := filepath.Join(root, "dir")
	write(t, filepath.Join(dir, "a"), "", 0o600)
	write(t, filepath.Join(dir, "b"), "", 0o600)

	if _, err := Look([]config.Gear{item(one, "~/one"), item(two, "~/two")}, Limits{Bytes: 11, Entries: 10}); err != nil {
		t.Fatalf("gear at the byte limit was refused: %v", err)
	}
	_, err := Look([]config.Gear{item(one, "~/one"), item(two, "~/two")}, Limits{Bytes: 10, Entries: 10})
	if err == nil || !strings.Contains(err.Error(), "the gear is larger than 10 bytes") {
		t.Errorf("error %v", err)
	}
	if _, err := Look([]config.Gear{item(one, "~/one"), item(dir, "~/dir")}, Limits{Bytes: 100, Entries: 4}); err != nil {
		t.Fatalf("gear at the entry limit was refused: %v", err)
	}
	_, err = Look([]config.Gear{item(one, "~/one"), item(dir, "~/dir")}, Limits{Bytes: 100, Entries: 3})
	if err == nil || !strings.Contains(err.Error(), "the gear has more than 3 files and directories") {
		t.Errorf("error %v", err)
	}
}

func TestAFileThatChangesAfterTheSurveyStopsTheArchive(t *testing.T) {
	root := t.TempDir()
	outside := write(t, filepath.Join(root, "outside"), "12345", 0o600)
	grown := write(t, filepath.Join(root, "grown"), "12345", 0o600)
	swapped := write(t, filepath.Join(root, "swapped"), "12345", 0o600)
	survey, err := Look([]config.Gear{item(grown, "~/grown"), item(swapped, "~/swapped")}, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}

	write(t, grown, "1234567890", 0o600)
	if err := os.Remove(swapped); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, swapped); err != nil {
		t.Fatal(err)
	}

	for _, source := range survey.Sources {
		var buffer bytes.Buffer
		err := source.Archive(&buffer)
		if err == nil || !strings.Contains(err.Error(), "changed while stage was copying it") {
			t.Errorf("%s: error %v", source.To, err)
		}
	}
}
