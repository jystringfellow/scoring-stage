package cli

import (
	"strings"
	"testing"
)

func attachReady(t *testing.T) *harness {
	t.Helper()
	h := readyToRead(t)
	serveCAO(h, "9889")
	h.sbx.SetPane("cao-alpha:review-5e6f", "Approve the draft? [a/s]\n")
	h.terminal = true
	return h
}

func execCallsSince(h *harness, before int) []string {
	var calls []string
	for _, call := range h.sbx.Calls()[before:] {
		if strings.HasPrefix(call, "exec ") {
			calls = append(calls, call)
		}
	}
	return calls
}

func TestAttachOpensACAOTerminalsTmuxWindowAsTheAgent(t *testing.T) {
	h := attachReady(t)
	h.expect(0, "attach", "fix", "5e6f7a8b")
	want := "exec -it -u agent -w " + h.repoDir() + " stage-fix tmux attach-session -t cao-alpha:review-5e6f"
	if got := h.sbx.LastCall(); got != want {
		t.Errorf("last call %q, want %q", got, want)
	}
	h.expectStderr("Ctrl-C")
	h.expectStderr("detach")
}

func TestAttachWithWatchCannotTypeIntoTheAgent(t *testing.T) {
	h := attachReady(t)
	h.expect(0, "attach", "fix", "5e6f7a8b", "--watch")
	want := "exec -it -u agent -w " + h.repoDir() + " stage-fix tmux attach-session -r -t cao-alpha:review-5e6f"
	if got := h.sbx.LastCall(); got != want {
		t.Errorf("last call %q, want %q", got, want)
	}
}

func TestAttachFindsATerminalOnACAOPortFromTheUsersConfig(t *testing.T) {
	h := attachReady(t)
	userConfig(t, h, "[hall]\ncao_ports = [9999]\n")
	serveCAO(h, "9999")
	h.sbx.SetCAO("9889", "/terminals/5e6f7a8b", "")
	h.expect(0, "attach", "fix", "5e6f7a8b")
	if got := h.sbx.LastCall(); !strings.HasSuffix(got, "tmux attach-session -t cao-alpha:review-5e6f") {
		t.Errorf("last call %q", got)
	}
}

func TestAttachRefusesAStoppedStageWithoutStartingIt(t *testing.T) {
	h := attachReady(t)
	h.sbx.SetSandboxes("")
	h.sbx.SetStopped("stage-fix\n")
	before := len(h.sbx.Calls())
	h.expect(1, "attach", "fix", "5e6f7a8b")
	h.expectStderr("not running")
	if calls := execCallsSince(h, before); len(calls) != 0 {
		t.Errorf("exec calls %q on a stopped stage", calls)
	}
}

func TestAttachRefusesAnythingButATerminalIDBeforeAskingSbx(t *testing.T) {
	h := attachReady(t)
	before := len(h.sbx.Calls())
	for _, id := range []string{"../x", "5E6F7A8B", "5e6f7a8", "-t", "5e6f7a8b;id"} {
		h.expect(2, "attach", "fix", id)
	}
	if calls := h.sbx.Calls()[before:]; len(calls) != 0 {
		t.Errorf("sbx calls %q", calls)
	}
}

func TestAttachRefusesATerminalTheStageDoesNotHave(t *testing.T) {
	h := attachReady(t)
	before := len(h.sbx.Calls())
	h.expect(1, "attach", "fix", "deadbeef")
	h.expectStderr("no CAO terminal deadbeef")
	for _, call := range execCallsSince(h, before) {
		if strings.Contains(call, "attach-session") {
			t.Errorf("attached: %q", call)
		}
	}
}

func TestAttachRefusesATmuxNameItCannotPassOnSafely(t *testing.T) {
	for name, terminal := range map[string]string{
		"window":  `{"id":"5e6f7a8b","name":"review:0;kill","session_name":"cao-alpha","status":"waiting_user_answer"}`,
		"session": `{"id":"5e6f7a8b","name":"review-5e6f","session_name":"-x","status":"waiting_user_answer"}`,
		"missing": `{"id":"5e6f7a8b","name":"review-5e6f","status":"waiting_user_answer"}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := attachReady(t)
			h.sbx.SetCAO("9889", "/terminals/5e6f7a8b", terminal)
			before := len(h.sbx.Calls())
			h.expect(1, "attach", "fix", "5e6f7a8b")
			h.expectStderr("tmux")
			for _, call := range execCallsSince(h, before) {
				if strings.Contains(call, "attach-session") {
					t.Errorf("attached: %q", call)
				}
			}
		})
	}
}

func TestAttachNeedsATerminal(t *testing.T) {
	h := attachReady(t)
	h.terminal = false
	before := len(h.sbx.Calls())
	h.expect(1, "attach", "fix", "5e6f7a8b")
	h.expectStderr("terminal")
	if calls := execCallsSince(h, before); len(calls) != 0 {
		t.Errorf("exec calls %q", calls)
	}
}
