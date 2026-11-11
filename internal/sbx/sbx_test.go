package sbx

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseVersion(t *testing.T) {
	for text, want := range map[string]Version{
		"sbx version v0.51.0 7d55286de0c775148b985ed349f760f1eba06203\n": {0, 51, 0},
		"v1.2.13": {1, 2, 13},
	} {
		got, err := ParseVersion(text)
		if err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v", text, got, err, want)
		}
	}
}

func TestUnreadableVersionIsAnError(t *testing.T) {
	if _, err := ParseVersion("sbx dev build"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestVersionFloor(t *testing.T) {
	for _, c := range []struct {
		found Version
		ok    bool
	}{{Version{0, 50, 9}, false}, {Version{0, 51, 0}, true}, {Version{1, 0, 0}, true}} {
		err := CheckVersion(c.found)
		if c.ok && err != nil {
			t.Errorf("%v: unexpected %v", c.found, err)
		}
		if !c.ok && (err == nil || !strings.Contains(err.Error(), "0.51.0 or later")) {
			t.Errorf("%v: got %v", c.found, err)
		}
	}
}

func rule(overrides map[string]any) map[string]any {
	r := map[string]any{"id": "r", "scope": "global", "decision": "allow", "resource_type": "network", "status": "active"}
	for k, v := range overrides {
		r[k] = v
	}
	return r
}

func TestGlobalAllowRulesIgnoreDeniesSandboxScopesDisabledRulesAndFilesystem(t *testing.T) {
	policy := Policy{Rules: []map[string]any{
		rule(map[string]any{"id": "default-ai-services"}),
		rule(map[string]any{"id": "http", "resource_type": "http"}),
		rule(map[string]any{"id": "deny", "decision": "deny"}),
		rule(map[string]any{"id": "scoped", "scope": "sandbox:stage-x"}),
		rule(map[string]any{"id": "off", "status": "disabled"}),
		rule(map[string]any{"id": "fs", "resource_type": "filesystem"}),
	}}
	if got := GlobalAllowRules(policy); !reflect.DeepEqual(got, []string{"default-ai-services", "http"}) {
		t.Fatalf("got %v", got)
	}
}

func TestRulesWithoutStatusOrIDAreActiveAndNamedQuestionMark(t *testing.T) {
	policy := Policy{Rules: []map[string]any{{"scope": "global", "decision": "allow", "resource_type": "network"}}}
	if got := GlobalAllowRules(policy); !reflect.DeepEqual(got, []string{"?"}) {
		t.Fatalf("got %v", got)
	}
}

func TestEmptyPolicyIsDenyAll(t *testing.T) {
	if got := GlobalAllowRules(Policy{}); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func fakeSbx(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sbx"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func readAll(output io.Reader) error {
	_, err := io.Copy(io.Discard, output)
	return err
}

func returnsWithin(t *testing.T, limit time.Duration, stream func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- stream() }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("Stream was still waiting after %s", limit)
		return nil
	}
}

func TestStreamHandsOverEverythingSbxWrites(t *testing.T) {
	fakeSbx(t, `printf 'all of it'`)
	var got []byte
	err := Stream(time.Minute, func(output io.Reader) (err error) {
		got, err = io.ReadAll(output)
		return err
	}, "exec", "box")
	if err != nil || string(got) != "all of it" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestStreamStopsAnSbxThatDoesNotFinishInTime(t *testing.T) {
	fakeSbx(t, `sleep 30`)
	err := returnsWithin(t, 10*time.Second, func() error {
		return Stream(200*time.Millisecond, readAll, "exec", "box")
	})
	if err == nil || !strings.Contains(err.Error(), "sbx exec box did not finish within 200ms") {
		t.Fatalf("error %v", err)
	}
}

func TestStreamReturnsTheConsumersErrorEvenWhileAChildOfSbxKeepsWriting(t *testing.T) {
	fakeSbx(t, `head -c 5000000 /dev/zero; sleep 30`)
	refused := errors.New("refused")
	err := returnsWithin(t, 10*time.Second, func() error {
		return Stream(time.Minute, func(output io.Reader) error {
			io.ReadFull(output, make([]byte, 512))
			return refused
		}, "exec", "box")
	})
	if !errors.Is(err, refused) {
		t.Fatalf("error %v", err)
	}
}

func TestStreamSaysWhatSbxSaidWhenItsOutputWasCutShort(t *testing.T) {
	fakeSbx(t, `printf abc; echo 'sandbox stopped' >&2; exit 1`)
	short := errors.New("unexpected end")
	err := Stream(time.Minute, func(output io.Reader) error {
		readAll(output)
		return short
	}, "exec", "box")
	if !errors.Is(err, short) || !strings.Contains(err.Error(), "sandbox stopped") {
		t.Fatalf("error %v", err)
	}
}

func TestStreamReportsAFailedSbxAfterACompleteRead(t *testing.T) {
	fakeSbx(t, `printf abc; echo 'no such sandbox' >&2; exit 1`)
	err := Stream(time.Minute, readAll, "exec", "box")
	if err == nil || err.Error() != "sbx exec box failed: no such sandbox" {
		t.Fatalf("error %v", err)
	}
}

func TestOnlyTheEndOfWhatSbxWritesToStderrIsKept(t *testing.T) {
	kept := &tail{limit: 16}
	for range 1000 {
		kept.Write([]byte("a long line of noise\n"))
	}
	kept.Write([]byte("the end"))
	if len(kept.kept) != 16 || !strings.HasSuffix(string(kept.kept), "the end") {
		t.Fatalf("kept %q", kept.kept)
	}
}

func TestStreamSucceedsWhenSbxExitsCleanlyButAChildKeepsItsStderrOpen(t *testing.T) {
	fakeSbx(t, `printf done; sleep 30 >/dev/null &`)
	err := returnsWithin(t, 10*time.Second, func() error {
		return Stream(time.Minute, readAll, "exec", "box")
	})
	if err != nil {
		t.Fatalf("error %v", err)
	}
}
