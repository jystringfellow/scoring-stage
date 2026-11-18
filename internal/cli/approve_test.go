package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func repositoryAskingForAKit(t *testing.T) *harness {
	t.Helper()
	h := setup(t)
	h.commitToMain(map[string]string{
		".scoring-stage/config.toml":         "kits = [\"./.scoring-stage/kits/rules\"]\ntakes = [\"~/notes\"]\n",
		".scoring-stage/kits/rules/kit.yaml": "allow: [example.com]\n",
	})
	return h
}

func (h *harness) answerAtATerminal(answer string) {
	h.terminal, h.stdin = true, answer
}

func (h *harness) noTerminal() {
	h.terminal, h.stdin = false, ""
}

func (h *harness) expectNoApprovalsRecorded() {
	h.t.Helper()
	if content, err := os.ReadFile(filepath.Join(h.home, "trusted-kits")); err == nil && strings.TrimSpace(string(content)) != "" {
		h.t.Fatalf("approvals were recorded:\n%s", content)
	}
}

func TestTheOwnerApprovesARepositoryOnceAndLaterUpsNeedNoFlagOrTerminal(t *testing.T) {
	h := repositoryAskingForAKit(t)
	h.answerAtATerminal("y\n")
	h.expect(0, "approve", h.remote.Checkout)
	h.expectStderr("asks for kits, books or takes you have not approved")
	h.expectStderr("  - kit ")
	h.expectStderr("  - takes for acme/api")
	if h.anyCall("env create", "env plan") {
		t.Fatalf("approve created or planned a sandbox: %v", h.sbx.Calls())
	}
	if entries, _ := os.ReadDir(filepath.Join(h.home, "tasks")); len(entries) != 0 {
		t.Fatalf("approve left a task behind: %v", entries)
	}

	h.noTerminal()
	h.expect(0, "up", h.remote.Checkout, "one")
	h.expect(0, "up", h.remote.Checkout, "two")

	d := readyMcp(t, h)
	toolData(t, d.tool("up", map[string]any{"repo": h.remote.Checkout, "slug": "three"}))
	d.close()

	h.answerAtATerminal("")
	h.expect(0, "approve", h.remote.Checkout)
	if !strings.Contains(h.stdout.String(), "already approved") {
		t.Errorf("stdout %q does not say the repository is already approved", h.stdout.String())
	}
}

func TestApproveRefusesWithoutATerminalAndRecordsNothing(t *testing.T) {
	h := repositoryAskingForAKit(t)
	h.terminal, h.stdin = false, "y\n"
	h.expect(1, "approve", h.remote.Checkout)
	h.expectStderr("in a terminal")
	h.expectNoApprovalsRecorded()

	h.noTerminal()
	h.expect(1, "up", h.remote.Checkout, "one")
	h.expectStderr("asks for kits, books or takes you have not approved")
	if h.anyCall("env create") {
		t.Fatal("created a sandbox with an unapproved repository kit")
	}
}

func TestApproveRecordsNothingUnlessTheAnswerIsYes(t *testing.T) {
	h := repositoryAskingForAKit(t)
	for _, answer := range []string{"n\n", "\n", "", "approve\n"} {
		h.answerAtATerminal(answer)
		h.expect(1, "approve", h.remote.Checkout)
		h.expectStderr("not approved")
		h.expectNoApprovalsRecorded()
	}
	h.noTerminal()
	h.expect(1, "up", h.remote.Checkout, "one")
}

func TestApproveHasNoFlagThatAnswersForThePerson(t *testing.T) {
	h := repositoryAskingForAKit(t)
	for _, flag := range []string{"--yes", "-y", "--approve-repo-config", "--force", "--no-input"} {
		h.answerAtATerminal("")
		h.expect(2, "approve", h.remote.Checkout, flag)
		h.expectNoApprovalsRecorded()
	}
}

func TestTheMcpToolsOfferNoWayToRecordAnApproval(t *testing.T) {
	h := repositoryAskingForAKit(t)
	d := readyMcp(t, h)
	listed := fmt.Sprint(d.request("tools/list", map[string]any{}))
	if strings.Contains(listed, "name:approve") {
		t.Fatalf("the tools list offers approve: %s", listed)
	}
	protocolError(t, d.request("tools/call", map[string]any{"name": "approve", "arguments": map[string]any{"repo": h.remote.Checkout}}), -32602)
	toolError(t, d.tool("up", map[string]any{"repo": h.remote.Checkout, "slug": "fix"}), "stage approve")
	d.close()
	h.expectNoApprovalsRecorded()
	if h.anyCall("env create") {
		t.Fatal("created a sandbox with an unapproved repository kit")
	}
}

func TestAnApprovalStopsCoveringARepositoryWhoseKitChanged(t *testing.T) {
	h := repositoryAskingForAKit(t)
	h.answerAtATerminal("y\n")
	h.expect(0, "approve", h.remote.Checkout)
	h.commitToMain(map[string]string{".scoring-stage/kits/rules/kit.yaml": "allow: [\"**\"]\n"})

	h.noTerminal()
	h.expect(1, "up", h.remote.Checkout, "one")
	h.expectStderr("asks for kits, books or takes you have not approved")
	if h.anyCall("env create") {
		t.Fatal("created a sandbox with a changed, unapproved repository kit")
	}
}

func TestApproveReviewsTheBranchItIsGiven(t *testing.T) {
	h := setup(t)
	testutil.RunGit(t, h.remote.Checkout, "checkout", "-q", "-b", "release")
	testutil.WriteFile(t, filepath.Join(h.remote.Checkout, ".scoring-stage", "config.toml"), "takes = [\"~/notes\"]\n")
	testutil.RunGit(t, h.remote.Checkout, "add", ".")
	testutil.RunGit(t, h.remote.Checkout, "commit", "-q", "-m", "Keep notes")
	testutil.RunGit(t, h.remote.Checkout, "push", "-q", "https://github.com/acme/api.git", "HEAD:release")

	h.answerAtATerminal("")
	h.expect(0, "approve", h.remote.Checkout)
	if !strings.Contains(h.stdout.String(), "asks for nothing that needs approval") {
		t.Errorf("stdout %q", h.stdout.String())
	}

	h.answerAtATerminal("y\n")
	h.expect(0, "approve", h.remote.Checkout, "--base", "release")
	h.expectStderr("  - takes for acme/api")
	h.noTerminal()
	h.expect(0, "up", h.remote.Checkout, "one", "--base", "release")
}

func TestApproveNeedsNoSandboxRuntimeState(t *testing.T) {
	h := repositoryAskingForAKit(t)
	h.sbx.SetPolicy(`[{"type":"network","decision":"allow","resources":"example.com"}]`)
	h.answerAtATerminal("y\n")
	h.expect(0, "approve", h.remote.Checkout)
	if calls := h.sbx.Calls(); len(calls) != 0 {
		t.Fatalf("approve ran sbx: %v", calls)
	}
}
