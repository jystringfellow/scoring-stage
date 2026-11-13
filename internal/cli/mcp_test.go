package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

type mcpDriver struct {
	t      *testing.T
	input  io.WriteCloser
	output *bufio.Reader
	done   chan int
	id     int
	closed bool
}

func mcpSession(t *testing.T, h *harness) *mcpDriver {
	t.Helper()
	in, send := io.Pipe()
	receive, out := io.Pipe()
	app := &App{Stdin: in, Stdout: out, Stderr: io.Discard,
		FindGh: func() string { return h.gh }, BrokerPID: func(string) int { return 0 },
		BrokerStart: func(string) error { return nil }, IsTerminal: func() bool { return false },
		StdinIsTerminal: func() bool { return false }, GitHubAPI: h.github.api(),
		HostAWSProfiles: func() ([]string, error) { return h.hostProfiles, nil },
	}
	d := &mcpDriver{t: t, input: send, output: bufio.NewReader(receive), done: make(chan int, 1)}
	go func() { code := app.Main([]string{"mcp"}); in.Close(); out.Close(); d.done <- code }()
	t.Cleanup(d.close)
	return d
}

func (d *mcpDriver) close() {
	if d.closed {
		return
	}
	d.closed = true
	d.input.Close()
	if code := <-d.done; code != 0 {
		d.t.Errorf("mcp exit = %d", code)
	}
}
func (d *mcpDriver) send(v any) {
	d.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		d.t.Fatal(err)
	}
	if _, err = fmt.Fprintf(d.input, "%s\n", b); err != nil {
		d.t.Fatal(err)
	}
}
func (d *mcpDriver) read() map[string]any {
	d.t.Helper()
	line, err := d.output.ReadBytes('\n')
	if err != nil {
		d.t.Fatal(err)
	}
	var v map[string]any
	if err = json.Unmarshal(line, &v); err != nil {
		d.t.Fatalf("non-protocol stdout %q: %v", line, err)
	}
	return v
}
func (d *mcpDriver) request(method string, params any) map[string]any {
	d.t.Helper()
	d.id++
	d.send(map[string]any{"jsonrpc": "2.0", "id": d.id, "method": method, "params": params})
	v := d.read()
	if v["id"] != float64(d.id) {
		d.t.Fatalf("id = %v, want %d", v, d.id)
	}
	return v
}
func (d *mcpDriver) initialize(version string) map[string]any {
	return d.request("initialize", map[string]any{"protocolVersion": version, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}})
}
func (d *mcpDriver) tool(name string, args any) map[string]any {
	d.t.Helper()
	r := d.request("tools/call", map[string]any{"name": name, "arguments": args})
	v, ok := r["result"].(map[string]any)
	if !ok {
		d.t.Fatalf("tool %s: %v", name, r)
	}
	return v
}
func toolData(t *testing.T, r map[string]any) map[string]any {
	t.Helper()
	if r["isError"] == true {
		t.Fatalf("tool failed: %v", r)
	}
	v, ok := r["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("no structured result: %v", r)
	}
	return v
}
func toolError(t *testing.T, r map[string]any, contains string) {
	t.Helper()
	if r["isError"] != true || !strings.Contains(fmt.Sprint(r), contains) {
		t.Fatalf("want tool error %q: %v", contains, r)
	}
}
func protocolError(t *testing.T, r map[string]any, code int) {
	t.Helper()
	e, ok := r["error"].(map[string]any)
	if !ok || e["code"] != float64(code) {
		t.Fatalf("want error %d: %v", code, r)
	}
}
func readyMcp(t *testing.T, h *harness) *mcpDriver {
	d := mcpSession(t, h)
	if d.initialize("2025-11-25")["error"] != nil {
		t.Fatal("initialize failed")
	}
	return d
}

func TestMcpInitializeNegotiatesTheProtocolVersionAndDeclaresOnlyTools(t *testing.T) {
	for _, version := range []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05", "future"} {
		t.Run(version, func(t *testing.T) {
			d := mcpSession(t, setup(t))
			r := d.initialize(version)
			result, ok := r["result"].(map[string]any)
			if !ok {
				t.Fatal(r)
			}
			want := version
			if version == "future" {
				want = "2025-11-25"
			}
			if result["protocolVersion"] != want {
				t.Fatal(result)
			}
			c := result["capabilities"].(map[string]any)
			if len(c) != 1 || c["tools"] == nil {
				t.Fatal(c)
			}
			if result["serverInfo"].(map[string]any)["name"] != "stage" {
				t.Fatal(result)
			}
			protocolError(t, d.initialize(version), -32600)
		})
	}
}
func TestMcpRequestsBeforeInitializeAreRefusedExceptPing(t *testing.T) {
	d := mcpSession(t, setup(t))
	if d.request("ping", nil)["error"] != nil {
		t.Fatal("ping")
	}
	protocolError(t, d.request("tools/list", nil), -32600)
}
func TestMcpToolsListCarriesEveryToolWithStrictSchemasAndHonestAnnotations(t *testing.T) {
	d := readyMcp(t, setup(t))
	r := d.request("tools/list", map[string]any{})
	tools := r["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, v := range tools {
		tool := v.(map[string]any)
		name := tool["name"].(string)
		names[name] = true
		if tool["title"] == "" || !strings.Contains(tool["description"].(string), "untrusted") {
			t.Fatal(tool)
		}
		s := tool["inputSchema"].(map[string]any)
		if s["type"] != "object" || s["additionalProperties"] != false {
			t.Fatal(s)
		}
		a := tool["annotations"].(map[string]any)
		for _, k := range []string{"readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"} {
			if _, ok := a[k].(bool); !ok {
				t.Fatal(a)
			}
		}
		for k := range s["properties"].(map[string]any) {
			for _, bad := range []string{"approv", "allow", "discard", "yes", "force", "aws", "profile", "policy", "global", "protected", "token", "secret", "login", "cpus", "memory", "plan"} {
				if strings.Contains(strings.ToLower(k), bad) {
					t.Fatalf("unsafe field %s", k)
				}
			}
			if k == "pr" {
				t.Fatal(k)
			}
		}
	}
	want := map[string]bool{}
	for _, n := range []string{"list", "up", "put", "exec", "job", "take", "ship", "down"} {
		want[n] = true
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatal(names)
	}
}
func TestMcpMalformedAndUnknownRequestsGetProtocolErrorsAndTheServerKeepsServing(t *testing.T) {
	d := readyMcp(t, setup(t))
	cases := []struct {
		line string
		code int
	}{{"{", -32700}, {"[]", -32600}, {`{"id":1,"method":"ping"}`, -32600}, {`{"jsonrpc":"2.0","id":1,"method":7}`, -32600}, {`{"jsonrpc":"2.0","id":null,"method":"ping"}`, -32600}, {strings.Repeat("x", (1<<20)+1), -32600}, {`{"jsonrpc":"2.0","id":1,"method":"unknown"}`, -32601}, {`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"shell","arguments":{}}}`, -32602}, {`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list","arguments":[]}}`, -32602}}
	for _, c := range cases {
		if _, err := fmt.Fprintln(d.input, c.line); err != nil {
			t.Fatal(err)
		}
		protocolError(t, d.read(), c.code)
	}
	d.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/unknown"})
	if d.request("ping", nil)["error"] != nil {
		t.Fatal("server stopped")
	}
}
func TestMcpBadArgumentsAndReservedInputsAreToolErrorsBeforeAnySbxCall(t *testing.T) {
	h := setup(t)
	d := readyMcp(t, h)
	cases := []struct {
		name string
		args any
	}{{"up", map[string]any{"repo": h.remote.Checkout, "slug": "fix", "approve_repo_config": true}}, {"up", map[string]any{"repo": h.remote.Checkout, "slug": "fix", "allow_global_policy": true}}, {"down", map[string]any{"slug": "fix", "discard_takes": true}}, {"ship", map[string]any{"slug": "fix", "allow_protected_paths": true}}, {"take", map[string]any{"slug": 7}}, {"take", map[string]any{}}, {"take", map[string]any{"slug": "../oops"}}, {"list", map[string]any{"unexpected": true}}, {"up", map[string]any{"repo": h.remote.Checkout, "slug": "fix", "agents": []string{"docker.io/example/kit:1"}}}, {"up", map[string]any{"repo": h.remote.Checkout, "slug": "fix", "books": []string{"./book"}}}}
	for _, c := range cases {
		toolError(t, d.tool(c.name, c.args), "")
	}
	if len(h.sbx.Calls()) != 0 {
		t.Fatal(h.sbx.Calls())
	}
}
func TestMcpUpNeverApprovesAndReturnsWhatAndHowThePersonApproves(t *testing.T) {
	h := setup(t)
	h.commitToMain(map[string]string{".scoring-stage/config.toml": "takes = [\"~/notes\"]\n"})
	d := readyMcp(t, h)
	r := d.tool("up", map[string]any{"repo": h.remote.Checkout, "slug": "fix", "agents": []string{"codex"}, "base": "main"})
	toolError(t, r, "--approve-repo-config")
	if !strings.Contains(fmt.Sprint(r), "takes for acme/api") || !strings.Contains(fmt.Sprint(r), "--agent=codex") {
		t.Fatal(r)
	}
	if _, err := os.Stat(filepath.Join(h.home, "trusted-kits")); !os.IsNotExist(err) {
		t.Fatal("tool wrote approvals", err)
	}
	if h.anyCall("env create") {
		t.Fatal("created unapproved sandbox")
	}
	d.close()
	h.expect(0, "up", h.remote.Checkout, "fix", "--approve-repo-config")
}
func TestMcpUpCreatesAndListMatchesTheCommand(t *testing.T) {
	h := setup(t)
	d := readyMcp(t, h)
	toolData(t, d.tool("up", map[string]any{"repo": h.remote.Checkout, "slug": "fix"}))
	task, err := loadTask(h.home, "fix")
	if err != nil || task.GitHubRepo != "acme/api" {
		t.Fatalf("%+v %v", task, err)
	}
	h.sbx.SetSandboxes("stage-fix\n")
	data := toolData(t, d.tool("list", map[string]any{}))
	d.close()
	h.expect(0, "list")
	if data["output"] != printable(h.stdout.String()) {
		t.Fatal(data, h.stdout.String())
	}
}
func TestMcpShipReturnsTheReviewAndCommandAndNeverPushes(t *testing.T) {
	h, box := shippedTask(t)
	boxCommit(t, box, "fix.txt", "A change")
	d := readyMcp(t, h)
	data := toolData(t, d.tool("ship", map[string]any{"slug": "fix"}))
	if data["pushed"] != false || data["needs_person"] != true || !strings.Contains(fmt.Sprint(data["diffstat"]), "fix.txt") || data["commits"] == "" || data["command"] != "stage ship fix" {
		t.Fatal(data)
	}
	task, _ := loadTask(h.home, "fix")
	if len(task.Ships) != 0 {
		t.Fatal(task.Ships)
	}
	branches := testutil.RunGit(t, h.remote.Bare, "branch")
	if strings.Contains(branches, "agent/") {
		t.Fatal(branches)
	}
	for _, branch := range []string{"main", "agent/fix/hidden\u200b"} {
		toolError(t, d.tool("ship", map[string]any{"slug": "fix", "branch": branch}), "refusing")
	}
	data = toolData(t, d.tool("ship", map[string]any{"slug": "fix", "ref": "HEAD", "branch": "agent/fix/review"}))
	if data["command"] != "stage ship fix --ref=HEAD --branch=agent/fix/review" {
		t.Fatal(data)
	}
}
func TestMcpShipRefusesProtectedPathsAndNothingToShip(t *testing.T) {
	for _, path := range []string{".github/workflows/check.yml", "deploy/run", ""} {
		t.Run(path, func(t *testing.T) {
			h, box := shippedTask(t)
			writeGitHubSetup(t, h.home, "[ship]\nprotected_paths = [\"deploy/\"]\n")
			if path != "" {
				boxCommit(t, box, path, "Touch a file")
			} else {
				testutil.RunGit(t, box, "reset", "--hard", "HEAD~1")
			}
			d := readyMcp(t, h)
			r := d.tool("ship", map[string]any{"slug": "fix"})
			if path != "" {
				toolError(t, r, "protected")
			} else {
				toolError(t, r, "nothing to ship")
			}
		})
	}
}
func TestMcpPutSharesTheCommandChecksAndRestrictsSourcesToItsWorkingDirectory(t *testing.T) {
	h, user := putReady(t)
	root := filepath.Join(user, "project")
	file := testutil.WriteFile(t, filepath.Join(root, "notes"), "hello")
	t.Chdir(root)
	d := readyMcp(t, h)
	toolData(t, d.tool("put", map[string]any{"slug": "fix", "file": file, "destination": "~/notes"}))
	if h.received("notes")["notes"] != "hello" {
		t.Fatal("copy changed")
	}
	outside := testutil.WriteFile(t, filepath.Join(user, "outside"), "x")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{outside, filepath.Join(root, "link")} {
		toolError(t, d.tool("put", map[string]any{"slug": "fix", "file": f}), "working directory")
	}
	toolError(t, d.tool("put", map[string]any{"slug": "fix", "file": file, "destination": filepath.Join(h.task("fix"), "src", "notes")}), "workspace")
	d.close()
	t.Chdir(user)
	d = readyMcp(t, h)
	toolError(t, d.tool("put", map[string]any{"slug": "fix", "file": file}), "working directory")
}
func TestMcpExecReturnsStatusAndBoundedEscapedOutput(t *testing.T) {
	h, _ := putReady(t)
	h.sbx.RunExecs()
	d := readyMcp(t, h)
	r := toolData(t, d.tool("exec", map[string]any{"slug": "fix", "command": []string{"sh", "-c", `printf '\033[31m\000\377'; head -c 70000 /dev/zero; printf err >&2; exit 3`}}))
	if r["status"] != float64(3) || r["stdout_bytes"] != float64(70007) || r["truncated"] != true || len(r["stdout"].(string)) > 140000 || r["stderr"] != "err" {
		t.Fatal(r)
	}
	if !strings.HasPrefix(r["stdout"].(string), `\x1b[31m\x00\xff`) {
		t.Fatalf("%q", r["stdout"].(string)[:30])
	}
}
func TestMcpDetachedJobsSurviveTheServerAndSupportListStatusWaitOutputAndStop(t *testing.T) {
	h, _ := jobsReady(t)
	d := readyMcp(t, h)
	gate, args := gated(t, h)
	r := toolData(t, d.tool("exec", map[string]any{"slug": "fix", "command": args[4:], "detach": true}))
	id := r["job"].(string)
	d.close()
	if j, err := readJob(h.home, "fix", id); err != nil || j.State != "running" {
		t.Fatal(j, err)
	}
	d = readyMcp(t, h)
	toolError(t, d.tool("down", map[string]any{"slug": "fix"}), "running")
	list := toolData(t, d.tool("job", map[string]any{"slug": "fix", "action": "list"}))
	if len(list["jobs"].([]any)) != 1 {
		t.Fatal(list)
	}
	status := toolData(t, d.tool("job", map[string]any{"slug": "fix", "action": "status", "id": id}))
	if status["state"] != "running" {
		t.Fatal(status)
	}
	testutil.WriteFile(t, gate, "")
	status = toolData(t, d.tool("job", map[string]any{"slug": "fix", "action": "wait", "id": id, "timeout_seconds": 5}))
	if status["status"] != float64(3) {
		t.Fatal(status)
	}
	out := toolData(t, d.tool("job", map[string]any{"slug": "fix", "action": "output", "id": id}))
	if out["text"] != "out\nerr\n" || out["total_bytes"] != float64(8) {
		t.Fatal(out)
	}
	r = toolData(t, d.tool("exec", map[string]any{"slug": "fix", "command": []string{"sh", "-c", "exec sleep 30"}, "detach": true}))
	id = r["job"].(string)
	status = toolData(t, d.tool("job", map[string]any{"slug": "fix", "action": "stop", "id": id}))
	if status["state"] != "stopped" {
		t.Fatal(status)
	}
	toolError(t, d.tool("job", map[string]any{"slug": "fix", "action": "output", "id": "../task.json"}), "job")
}
func TestMcpJobOutputIsPagedWithoutSplittingRunesAndEscapesInvalidBytes(t *testing.T) {
	h, _ := putReady(t)
	dir := jobDir(h.home, "fix", "1")
	testutil.WriteFile(t, filepath.Join(dir, "job.json"), `{"id":"1","slug":"fix","command":["x\u001b"],"started_at":"now"}`)
	testutil.WriteFile(t, filepath.Join(dir, "exit.json"), `{"status":0,"ended_at":"then","output_truncated":true}`)
	testutil.WriteFile(t, filepath.Join(dir, "output"), "a€\x1b\xff"+strings.Repeat("q", 100000))
	d := readyMcp(t, h)
	r := toolData(t, d.tool("job", map[string]any{"slug": "fix", "action": "output", "id": "1", "limit": 3}))
	if r["text"] != "a" || r["next_offset"] != float64(1) || r["total_bytes"] != float64(100006) {
		t.Fatal(r)
	}
	r = toolData(t, d.tool("job", map[string]any{"slug": "fix", "action": "output", "id": "1", "offset": 1, "limit": 999999}))
	if !strings.HasPrefix(r["text"].(string), `€\x1b\xff`) || len(r["text"].(string)) > 66000 || r["output_truncated"] != true {
		t.Fatal(r)
	}
	r = toolData(t, d.tool("job", map[string]any{"slug": "fix", "action": "status", "id": "1"}))
	if r["command"].([]any)[0] != `x\x1b` {
		t.Fatal(r)
	}
}

// Re-exec uses the same entry point as job holders, with real stdio pipes.
func mcpProcess(t *testing.T, h *harness) *mcpDriver {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "mcp")
	cmd.Env = append(os.Environ(), "STAGE_TEST_AS_STAGE=1")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	d := &mcpDriver{t: t, input: in, output: bufio.NewReader(out), done: make(chan int, 1)}
	go func() {
		err := cmd.Wait()
		if err != nil {
			d.done <- 1
		} else {
			d.done <- 0
		}
	}()
	t.Cleanup(d.close)
	return d
}
func TestMcpWritesOnlyProtocolToStdoutAndNeverLetsChildrenReadTheClientPipe(t *testing.T) {
	h, user := putReady(t)
	h.sandboxCommit("fix")
	h.sbx.Noisy()
	h.sbx.RunExecs()
	root := filepath.Join(user, "project")
	file := testutil.WriteFile(t, filepath.Join(root, "notes"), "hello")
	t.Chdir(root)
	d := mcpProcess(t, h)
	d.initialize("2025-11-25")
	toolData(t, d.tool("up", map[string]any{"repo": h.remote.Checkout, "slug": "other"}))
	toolData(t, d.tool("put", map[string]any{"slug": "fix", "file": file}))
	toolData(t, d.tool("ship", map[string]any{"slug": "fix"}))
	r := toolData(t, d.tool("exec", map[string]any{"slug": "fix", "command": []string{"printf", "process"}, "detach": true}))
	toolData(t, d.tool("job", map[string]any{"slug": "fix", "id": r["job"], "action": "wait", "timeout_seconds": 5}))
	d.close()
}

func TestMcpExecTimeoutAndJobWaitAreCapped(t *testing.T) {
	h, _ := jobsReady(t)
	oldExec, oldWait := mcpExecMax, mcpWaitMax
	mcpExecMax, mcpWaitMax = 100*time.Millisecond, 100*time.Millisecond
	defer func() { mcpExecMax, mcpWaitMax = oldExec, oldWait }()
	d := readyMcp(t, h)
	start := time.Now()
	finished := filepath.Join(t.TempDir(), "finished")
	r := toolData(t, d.tool("exec", map[string]any{"slug": "fix", "command": []string{"sh", "-c", `sleep 0.3; touch "$1"`, "sh", finished}, "timeout_seconds": 3600}))
	if r["timed_out"] != true || r["status"] != nil || r["capped"] != true || time.Since(start) > 2*time.Second {
		t.Fatal(r, time.Since(start))
	}
	// The fake's child can outlive the killed client, just as a sandbox command can.
	eventually(t, func() bool { _, err := os.Stat(finished); return err == nil })
	gate, args := gated(t, h)
	r = toolData(t, d.tool("exec", map[string]any{"slug": "fix", "command": args[4:], "detach": true}))
	id := r["job"].(string)
	start = time.Now()
	r = toolData(t, d.tool("job", map[string]any{"slug": "fix", "action": "wait", "id": id, "timeout_seconds": 604800}))
	if r["state"] != "running" || r["capped"] != true || time.Since(start) > time.Second {
		t.Fatal(r)
	}
	testutil.WriteFile(t, gate, "")
	d.close()
	h.expect(3, "job", "wait", "fix", id, "--timeout", "5s")
}

func TestMcpCancellingAWaitSendsNoResponseAndEofCancelsAnInflightWait(t *testing.T) {
	h, _ := jobsReady(t)
	d := readyMcp(t, h)
	gate, args := gated(t, h)
	r := toolData(t, d.tool("exec", map[string]any{"slug": "fix", "command": args[4:], "detach": true}))
	id := r["job"].(string)
	d.send(map[string]any{"jsonrpc": "2.0", "id": "cancel-me", "method": "tools/call", "params": map[string]any{"name": "job", "arguments": map[string]any{"action": "wait", "slug": "fix", "id": id, "timeout_seconds": 50}}})
	d.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": "cancel-me"}})
	if r := d.request("ping", map[string]any{}); r["error"] != nil {
		t.Fatal(r)
	}
	// Let another bounded wait finish so an incorrectly emitted cancellation response is observable.
	r = toolData(t, d.tool("job", map[string]any{"action": "wait", "slug": "fix", "id": id, "timeout_seconds": 1}))
	if r["state"] != "running" {
		t.Fatal(r)
	}
	d.send(map[string]any{"jsonrpc": "2.0", "id": "eof", "method": "tools/call", "params": map[string]any{"name": "job", "arguments": map[string]any{"action": "wait", "slug": "fix", "id": id, "timeout_seconds": 50}}})
	start := time.Now()
	d.close()
	if time.Since(start) > time.Second {
		t.Fatal("EOF did not cancel wait")
	}
	if j, err := readJob(h.home, "fix", id); err != nil || j.State != "running" {
		t.Fatal(j, err)
	}
	testutil.WriteFile(t, gate, "")
	h.expect(3, "job", "wait", "fix", id, "--timeout", "5s")
}

func TestMcpTakeAndDownKeepTakesAndRefuseFailedCopies(t *testing.T) {
	for _, action := range []string{"take", "down", "failed-down"} {
		t.Run(action, func(t *testing.T) {
			h := takingSandbox(t, "fix")
			h.sbx.SetTakes(takeArchive(t, takeEntry{name: "home/agent/.tool/runs/one/result.json", body: "{}"}))
			d := readyMcp(t, h)
			if action == "failed-down" {
				h.sbx.FailTakes("copy failed")
				toolError(t, d.tool("down", map[string]any{"slug": "fix"}), "left in place")
				if h.anyCall("env rm", "rm") {
					t.Fatal(h.sbx.Calls())
				}
				return
			}
			toolData(t, d.tool(action, map[string]any{"slug": "fix"}))
			kept := h.kept("fix")
			if len(kept) != 1 {
				t.Fatal(kept)
			}
			if body := testutil.ReadFile(t, filepath.Join(h.home, "takes", "fix", kept[0], "home/agent/.tool/runs/one/result.json")); body != "{}" {
				t.Fatal(body)
			}
			_, err := os.Stat(h.task("fix"))
			if action == "down" && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		})
	}
}

func TestMcpUpNeverRewritesLegacyApprovalsAndCanOnlyUseInstalledNames(t *testing.T) {
	h := setup(t)
	h.installBook("example", "takes = [\"~/notes\"]\n")
	h.commitToMain(map[string]string{".scoring-stage/config.toml": "takes = [\"~/runs\"]\n"})
	legacy := strings.Repeat("a", 64) + " kit old\n"
	path := testutil.WriteFile(t, filepath.Join(h.home, "trusted-kits"), legacy)
	d := readyMcp(t, h)
	toolError(t, d.tool("up", map[string]any{"slug": "fix", "repo": h.remote.Checkout, "books": []string{"example"}}), "not approved")
	if got := testutil.ReadFile(t, path); got != legacy {
		t.Fatal("rewrote approvals", got)
	}
	if r := fmt.Sprint(d.tool("up", map[string]any{"slug": "fix", "repo": h.remote.Checkout, "books": []string{"not-installed"}})); !strings.Contains(r, "installed name") {
		t.Fatal(r)
	}
}

func TestMcpUpTreatsARepositoryThatLooksLikeAFlagAsAPath(t *testing.T) {
	h := setup(t)
	parent := t.TempDir()
	repo := filepath.Join(parent, "--allow-global-policy")
	testutil.RunGit(t, parent, "clone", "-q", h.remote.Checkout, repo)
	testutil.RunGit(t, repo, "remote", "set-url", "origin", "git@github.com:acme/api.git")
	t.Chdir(parent)
	h.sbx.SetPolicy(`{"rules":[{"scope":"global","decision":"allow","resource_type":"network","id":"one"}]}`)
	d := readyMcp(t, h)
	toolError(t, d.tool("up", map[string]any{"slug": "fix", "repo": "--allow-global-policy"}), "not deny-all")
	if h.anyCall("env create") {
		t.Fatal(h.sbx.Calls())
	}
}

func TestMcpAToolPanicIsAnErrorResultAndTheServerKeepsServing(t *testing.T) {
	h := setup(t)
	h.gh = ""
	old := config.ManagedPaths
	config.ManagedPaths = func() []string { panic("test panic") }
	defer func() { config.ManagedPaths = old }()
	d := readyMcp(t, h)
	toolError(t, d.tool("up", map[string]any{"slug": "fix", "repo": h.remote.Checkout}), "unexpectedly")
	if d.request("ping", nil)["error"] != nil {
		t.Fatal("server died")
	}
	d.close()
}

func TestMcpEveryResponseIsBoundedIncludingItsDuplicateTextContent(t *testing.T) {
	h, _ := putReady(t)
	h.sbx.RunExecs()
	d := readyMcp(t, h)
	r := d.tool("exec", map[string]any{"slug": "fix", "command": []string{"sh", "-c", "head -c 100000 /dev/zero; head -c 100000 /dev/zero >&2"}})
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 256<<10 {
		t.Fatal("oversized response", len(data))
	}
	v := toolData(t, r)
	if v["stdout_bytes"] != float64(100000) || v["stderr_bytes"] != float64(100000) || v["response_truncated"] != true {
		t.Fatal(v)
	}
	content := r["content"].([]any)[0].(map[string]any)["text"].(string)
	var duplicate any
	if err := json.Unmarshal([]byte(content), &duplicate); err != nil || !reflect.DeepEqual(duplicate, v) {
		t.Fatal("text and structured results disagree", err)
	}
}

func TestMcpRejectsInvalidArgvTypesNullsAndHostGitOptions(t *testing.T) {
	h, _ := putReady(t)
	d := readyMcp(t, h)
	for _, args := range []map[string]any{
		{"slug": "fix", "command": []string{}}, {"slug": "fix", "command": []string{""}},
		{"slug": "fix", "command": []any{"x", nil}}, {"slug": "fix", "command": []string{"x\x00"}},
		{"slug": "fix", "command": []string{"x"}, "timeout_seconds": -1},
		{"slug": "fix", "command": []string{"x"}, "timeout_seconds": nil},
	} {
		toolError(t, d.tool("exec", args), "")
	}
	toolError(t, d.tool("ship", map[string]any{"slug": "fix", "ref": "--upload-pack=example"}), "invalid sandbox ref")
}

func TestMcpAnArgumentlessToolCanOmitArguments(t *testing.T) {
	d := readyMcp(t, setup(t))
	r := d.request("tools/call", map[string]any{"name": "list"})
	if r["error"] != nil {
		t.Fatal(r)
	}
	toolData(t, r["result"].(map[string]any))
}

func TestMcpOutputPagesNeverSkipBytesWhenEscapingReachesTheResponseCap(t *testing.T) {
	h, _ := putReady(t)
	dir := jobDir(h.home, "fix", "1")
	testutil.WriteFile(t, filepath.Join(dir, "job.json"), `{"id":"1","slug":"fix","command":["x"]}`)
	testutil.WriteFile(t, filepath.Join(dir, "exit.json"), `{"status":0,"ended_at":"then"}`)
	testutil.WriteFile(t, filepath.Join(dir, "output"), strings.Repeat("\x00", 100000))
	d := readyMcp(t, h)
	r := toolData(t, d.tool("job", map[string]any{"slug": "fix", "action": "output", "id": "1", "limit": 65536}))
	next := int(r["next_offset"].(float64))
	if r["text"] != strings.Repeat(`\x00`, next) {
		t.Fatal("response truncated text but skipped bytes", next, len(r["text"].(string)))
	}
	if next <= 0 || next > 65536 || r["total_bytes"] != float64(100000) {
		t.Fatal(r)
	}
}

func TestMcpARequestIdCannotMakeTheResponseExceedItsLimit(t *testing.T) {
	d := readyMcp(t, setup(t))
	d.send(map[string]any{"jsonrpc": "2.0", "id": strings.Repeat("q", 300000), "method": "ping"})
	r := d.read()
	protocolError(t, r, -32600)
	if r["id"] != nil {
		t.Fatal("oversized id was echoed")
	}
	if d.request("ping", nil)["error"] != nil {
		t.Fatal("server stopped")
	}
}

func TestMcpChildCapturesDefaultBrokerOutputInsteadOfWritingToTheProtocol(t *testing.T) {
	var protocol, output strings.Builder
	app := New()
	app.Stdout = &protocol
	app.BrokerPID = func(string) int { return 123 }
	child := (&mcpServer{app: app}).child(&output, io.Discard)
	if _, err := child.cmdBroker([]string{"start"}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if protocol.Len() != 0 || !strings.Contains(output.String(), "broker already running") {
		t.Fatal("broker output escaped the tool capture")
	}
}

func TestMcpUpSelectsNamedAgentsInstalledBooksAndTheBaseBranch(t *testing.T) {
	h := setup(t)
	h.installBook("example", "takes = [\"~/notes\"]\n")
	d := readyMcp(t, h)
	toolData(t, d.tool("up", map[string]any{"repo": h.remote.Checkout, "slug": "fix", "agents": []string{"codex"}, "books": []string{"example"}, "base": "main"}))
	task, err := loadTask(h.home, "fix")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(task.Agents, []string{"codex"}) || task.BaseBranch != "main" || !reflect.DeepEqual(task.Takes, []string{"~/notes"}) {
		t.Fatalf("%+v", task)
	}
}

func TestMcpPutKeepsTheCredentialAndManagedSettingsRefusals(t *testing.T) {
	h, user := putReady(t)
	root := filepath.Join(user, "project")
	file := testutil.WriteFile(t, filepath.Join(root, "notes"), "hello")
	credential := testutil.WriteFile(t, filepath.Join(root, "credential"), "dummy")
	t.Setenv("KUBECONFIG", credential)
	t.Chdir(root)
	d := readyMcp(t, h)
	toolError(t, d.tool("put", map[string]any{"slug": "fix", "file": credential}), "credential")
	testutil.WriteFile(t, h.managed, "allow_gear = false\n")
	toolError(t, d.tool("put", map[string]any{"slug": "fix", "file": file}), "refused by")
	if h.anyCall("exec -i") {
		t.Fatal("copied a refused source")
	}
}

func TestMcpShipEscapesUntrustedCommitSubjects(t *testing.T) {
	h, box := shippedTask(t)
	boxCommit(t, box, "more.txt", "subject\x1b[31m\u200b")
	d := readyMcp(t, h)
	r := toolData(t, d.tool("ship", map[string]any{"slug": "fix"}))
	if !strings.Contains(r["commits"].(string), `subject\x1b[31m\x200b`) {
		t.Fatal(r)
	}
}
