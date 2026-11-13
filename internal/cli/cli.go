package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/jystringfellow/scoring-stage/internal/broker"
	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/envfile"
	"github.com/jystringfellow/scoring-stage/internal/github"
	"github.com/jystringfellow/scoring-stage/internal/kits"
	"github.com/jystringfellow/scoring-stage/internal/sbx"
	"github.com/jystringfellow/scoring-stage/internal/source"
)

const sandboxPrefix = "stage-"

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func errorf(format string, args ...any) error { return &Error{fmt.Sprintf(format, args...)} }

type exitCode int

func (e exitCode) Error() string { return "exit status " + strconv.Itoa(int(e)) }

type App struct {
	Stdin           io.Reader
	Stdout, Stderr  io.Writer
	FindGh          func() string
	BrokerPID       func(home string) int
	BrokerStart     func(home string) error
	IsTerminal      func() bool
	HostAWSProfiles func() ([]string, error)
	GitHub          *github.Client
	StdinIsTerminal func() bool
	ReadSecret      func() ([]byte, error)
	GitHubAPI       config.GitHubAPI
}

func New() *App {
	app := &App{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, FindGh: config.FindGh, BrokerPID: brokerPID, GitHub: github.NewClient()}
	app.BrokerStart = app.brokerStart
	app.HostAWSProfiles = hostAWSProfiles
	app.StdinIsTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	app.ReadSecret = func() ([]byte, error) { return term.ReadPassword(int(os.Stdin.Fd())) }
	app.GitHubAPI = config.NewGitHubAPI()
	app.IsTerminal = func() bool {
		return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	}
	return app
}

type Task struct {
	Slug       string   `json:"slug"`
	Sandbox    string   `json:"sandbox"`
	RepoDir    string   `json:"repo_dir"`
	GitHubRepo string   `json:"github_repo"`
	SourceRepo string   `json:"source_repo"`
	Agents     []string `json:"agents,omitempty"`
	GitConfig  string   `json:"git_config,omitempty"`
	Takes      []string `json:"takes,omitempty"`
	BaseBranch string   `json:"base_branch,omitempty"`
	Ships      []Ship   `json:"ships,omitempty"`
}

type Ship struct {
	SHA         string `json:"sha"`
	Branch      string `json:"branch"`
	GitHubRepo  string `json:"github_repo"`
	At          string `json:"at"`
	PullRequest string `json:"pull_request,omitempty"`
}

const usage = `usage: stage <command> [options]

Run coding agents in Docker Sandboxes with host-held credentials.

commands:
  setup [--yes] [--agent NAME]... [--github MODE]
                                   check sbx, the global policy, credentials and books; save your agents
  token set <owner>                 save a fine-grained GitHub token for a repository owner, read from stdin
  broker start|stop|status|serve    manage the host AWS credential broker
  github login|token                log in to your GitHub App ([github] mode = "app"); print a fresh token
  init [<repo>] [options]           draft and check a repository config and kits
  up <repo> <slug> [options]        create a sandbox for a repository
  shell <slug>                      open a login shell in the repository
  login <slug>                      save the sandbox's copyable agent logins (copy_logins) for future sandboxes
  exec <slug> -- <command> [args]   run a command in the repository
  exec --detach <slug> -- <command> [args] start a job held open by a host process
  job list <slug> [--json]           list jobs
  job status|wait|output|stop <slug> <id> [options]
                                    read, wait for, follow or stop a job
  put <slug> <file> [<destination>] copy a host file into a running sandbox
  ship <slug> [--ref R] [--branch B] [--pr]
                                    push the sandbox's commit to <prefix><slug>/<sha> from the host
                                    (prefix: [ship] branch_prefix, agent/ unless set)
  take <slug>                       copy the sandbox's takes to ~/.scoring-stage/takes/<slug>/ now
  list                              list scoring-stage sandboxes
  down <slug> [--discard-takes]     keep the sandbox's takes, then remove it, its secrets and the scratch clone
  version                           print the stage version
`

func (a *App) Main(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.Stderr, usage)
		fmt.Fprintln(a.Stderr, "stage: error: the following arguments are required: command")
		return 2
	}
	commands := map[string]func([]string, string) (int, error){
		"init": a.cmdInit, "setup": a.cmdSetup, "broker": a.cmdBroker, "up": a.cmdUp, "shell": a.cmdShell,
		"exec": a.cmdExec, "ship": a.cmdShip, "list": a.cmdList, "down": a.cmdDown, "login": a.cmdLogin, "take": a.cmdTake,
		"github": a.cmdGitHub,
		"token":  a.cmdToken,
		"put":    a.cmdPut, "job": a.cmdJob,
	}
	name := args[0]
	if name == "-h" || name == "--help" || name == "help" {
		fmt.Fprint(a.Stdout, usage)
		return 0
	}
	if name == "version" || name == "--version" {
		fmt.Fprintf(a.Stdout, "stage %s\n", versionOf(Version, debug.ReadBuildInfo))
		return 0
	}
	command, ok := commands[name]
	if !ok {
		fmt.Fprint(a.Stderr, usage)
		fmt.Fprintf(a.Stderr, "stage: error: invalid command %q\n", name)
		return 2
	}
	code, err := command(args[1:], config.StageHome())
	var usageErr *usageError
	var exit exitCode
	switch {
	case err == nil:
		return code
	case isHelp(err):
		return 0
	case errors.As(err, &usageErr):
		fmt.Fprintf(a.Stderr, "stage %s: error: %s\n", name, usageErr.msg)
		return 2
	case errors.As(err, &exit):
		return int(exit)
	default:
		fmt.Fprintf(a.Stderr, "stage: %s\n", err)
		return 1
	}
}

func (a *App) say(format string, args ...any) {
	fmt.Fprintf(a.Stdout, "stage: "+format+"\n", args...)
}

func (a *App) warn(format string, args ...any) {
	fmt.Fprintf(a.Stderr, "stage: WARNING: "+format+"\n", args...)
}

// confirm asks a yes-or-no question on the terminal; without one the answer is no.
func (a *App) confirm(question string) bool {
	if !a.IsTerminal() || a.Stdin == nil {
		return false
	}
	fmt.Fprintf(a.Stderr, "%s [y/N] ", question)
	answer, _ := bufio.NewReader(a.Stdin).ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

func sandboxOf(slug string) string { return sandboxPrefix + slug }

func checkSlug(slug string) (string, error) {
	if !slugPattern.MatchString(slug) {
		return "", errorf("slug must be lowercase letters, digits and dashes (at most 41): %s", slug)
	}
	return slug, nil
}

func taskDir(home, slug string) string { return filepath.Join(home, "tasks", slug) }

func grantsDir(home string) string { return filepath.Join(home, "broker", "grants") }

func loadTask(home, slug string) (*Task, error) {
	if _, err := checkSlug(slug); err != nil {
		return nil, err
	}
	content, err := os.ReadFile(filepath.Join(taskDir(home, slug), "task.json"))
	if os.IsNotExist(err) {
		return nil, errorf("no task %s; run stage up first", slug)
	}
	if err != nil {
		return nil, errorf("task %s is unreadable: %v", slug, err)
	}
	var task Task
	if err := json.Unmarshal(content, &task); err != nil {
		return nil, errorf("task %s is unreadable: %v", slug, err)
	}
	if task.Slug != slug {
		return nil, errorf("task %s is unreadable: its record does not name it", slug)
	}
	return &task, nil
}

func brokerAnswers() bool {
	connection, err := net.DialTimeout("tcp", broker.Address, time.Second)
	if err != nil {
		return false
	}
	connection.Close()
	return true
}

func brokerPID(home string) int {
	content, err := os.ReadFile(filepath.Join(home, "broker", "broker.pid"))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil || pid <= 0 {
		return 0
	}
	if syscall.Kill(pid, 0) != nil {
		return 0
	}
	return pid
}

func (a *App) checkHostAWSProfile(profile string) error {
	known, err := a.HostAWSProfiles()
	if err != nil {
		return err
	}
	if !slices.Contains(known, profile) {
		return errorf("--aws-profile %s is not a profile in your AWS config; `aws configure list-profiles` lists them", profile)
	}
	return nil
}

func hostAWSProfiles() ([]string, error) {
	if _, err := exec.LookPath("aws"); err != nil {
		return nil, errorf("the aws cli is not on PATH; --aws-profile needs it")
	}
	output, err := exec.Command("aws", "configure", "list-profiles").Output()
	if err != nil {
		return nil, errorf("aws configure list-profiles: %v", err)
	}
	return strings.Fields(string(output)), nil
}

func (a *App) brokerStart(home string) error {
	if pid := a.BrokerPID(home); pid != 0 {
		a.say("broker already running (pid %d)", pid)
		return nil
	}
	if brokerAnswers() {
		return errorf("%s is in use by another process", broker.Address)
	}
	if _, err := exec.LookPath("aws"); err != nil {
		return errorf("the aws cli is not on PATH; the broker needs it")
	}
	state := filepath.Join(home, "broker")
	if err := os.MkdirAll(grantsDir(home), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(state, 0o700); err != nil {
		return err
	}
	logPath := filepath.Join(state, "broker.log")
	log, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	process := exec.Command(self, "broker", "serve")
	process.Env = append(os.Environ(), "STAGE_HOME="+home)
	process.Stdout, process.Stderr = log, log
	process.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := process.Start(); err != nil {
		return err
	}
	pid := process.Process.Pid
	process.Process.Release()
	if err := os.WriteFile(filepath.Join(state, "broker.pid"), []byte(strconv.Itoa(pid)), 0o600); err != nil {
		return err
	}
	for range 20 {
		if brokerAnswers() {
			a.say("broker started (pid %d, log %s)", pid, logPath)
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errorf("broker did not start; see %s", logPath)
}

func (a *App) cmdBroker(args []string, home string) (int, error) {
	fs := a.newFlags("broker", "start|stop|status|serve")
	positional, err := fs.parse(args, "action")
	if err != nil {
		return 0, err
	}
	switch positional[0] {
	case "start":
		return 0, a.BrokerStart(home)
	case "stop":
		if pid := a.BrokerPID(home); pid != 0 {
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
				return 0, err
			}
			a.say("broker stopped (pid %d)", pid)
		} else {
			a.say("broker not running")
		}
		if err := os.Remove(filepath.Join(home, "broker", "broker.pid")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
		return 0, nil
	case "status":
		return a.brokerStatus(home), nil
	case "serve":
		return 0, broker.Serve(grantsDir(home), a.Stdout)
	}
	return 0, &usageError{fmt.Sprintf("argument action: invalid choice: %q (choose from start, stop, status, serve)", positional[0])}
}

func (a *App) brokerStatus(home string) int {
	if pid := a.BrokerPID(home); pid != 0 {
		a.say("broker running (pid %d) on %s", pid, broker.Address)
		return 0
	}
	if brokerAnswers() {
		a.say("broker not running, but %s is in use by another process", broker.Address)
		return 1
	}
	a.say("broker not running")
	return 1
}

func (a *App) cmdSetup(args []string, home string) (int, error) {
	fs := a.newFlags("setup", "[--yes] [--agent NAME]... [--github MODE]")
	yes := fs.Bool("yes", false, "reset the global sbx policy to deny-all")
	var agentFlags stringList
	fs.Var(&agentFlags, "agent", "save an agent to config.toml: "+strings.Join(config.AgentNames(home), ", ")+" (repeatable)")
	githubMode := fs.String("github", "", "save how sandboxes reach GitHub to config.toml: "+strings.Join(config.GitHubModes, ", "))
	if _, err := fs.parse(args); err != nil {
		return 0, err
	}
	found, err := sbx.RequireVersion()
	if err != nil {
		return 0, err
	}
	a.say("sbx %s (ok)", found)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return 0, err
	}
	if err := os.Chmod(home, 0o700); err != nil {
		return 0, err
	}
	a.say("state dir: %s", home)
	allows, err := sbx.GlobalAllows()
	if err != nil {
		return 0, err
	}
	switch {
	case len(allows) == 0:
		a.say("global sbx policy: deny-all (ok)")
	case *yes:
		a.say("resetting the global sbx policy to deny-all (this stops running sandboxes)")
		if err := sbx.Run("policy", "reset", "--force"); err != nil {
			return 0, err
		}
		if err := sbx.Run("policy", "init", "deny-all"); err != nil {
			return 0, err
		}
		if remaining, err := sbx.GlobalAllows(); err != nil {
			return 0, err
		} else if len(remaining) > 0 {
			return 0, errorf("the global policy still has allow rules after the reset")
		}
	default:
		a.warn("the global sbx policy allows traffic for every sandbox (%d rules: %s)", len(allows), strings.Join(allows, ", "))
		fmt.Fprint(a.Stderr, "  scoring-stage boxes get their egress from kits; global allow rules widen every box.\n"+
			"  `stage setup --yes` runs `sbx policy reset --force` (deletes ALL local policies and stops\n"+
			"  running sandboxes) and then `sbx policy init deny-all`.\n")
	}
	if *githubMode != "" && !slices.Contains(config.GitHubModes, *githubMode) {
		return 0, errorf("--github must be one of %s", strings.Join(config.GitHubModes, ", "))
	}
	if *githubMode != "" {
		managed, err := config.LoadManaged()
		if err != nil {
			return 0, err
		}
		if err := managed.CheckGitHubMode(*githubMode); err != nil {
			return 0, errorf("--github: %v", err)
		}
	}
	if len(agentFlags) > 0 {
		if err := config.SaveAgents(home, agentFlags); err != nil {
			return 0, err
		}
		a.say("saved agents %s to %s", strings.Join(agentFlags, ", "), filepath.Join(home, "config.toml"))
	}
	if *githubMode != "" {
		if err := config.SaveGitHubMode(home, *githubMode); err != nil {
			return 0, err
		}
		a.say("saved [github] mode = %q to %s", *githubMode, filepath.Join(home, "config.toml"))
	}
	settings, err := config.LoadSettings(home)
	if err != nil {
		return 0, err
	}
	for _, line := range settings.Managed.Summary() {
		a.say("%s", line)
	}
	for _, warning := range settings.Managed.Warnings() {
		a.warn("managed settings: %s", warning)
	}
	if agentList := settings.AgentEntries(); len(agentList) > 0 {
		a.say("agents: %s", strings.Join(agentList, ", "))
	} else {
		a.warn("no agents in config.toml; boxes get only a shell unless --agent, a book or the repository adds some (stage setup --agent %s ...)", settings.Agents.Names()[0])
	}
	var bookNames []string
	for _, book := range settings.Books {
		bookNames = append(bookNames, book.Name)
	}
	if len(bookNames) == 0 {
		bookNames = []string{"none"}
	}
	a.say("installed books: %s", strings.Join(bookNames, ", "))
	if _, err := settings.CheckedGear(); err != nil {
		a.warn("%s; stage up will refuse", err)
	} else if len(settings.Gear) > 0 {
		a.say("gear, copied into every new sandbox for the agent to read: %s", config.GearSummary(settings.Gear))
	}
	gh := a.FindGh()
	ghLoggedIn := gh != "" && exec.Command(gh, "auth", "token").Run() == nil
	if err := a.reportGitHub(settings, gh, ghLoggedIn); err != nil {
		return 0, err
	}
	if err := checkNoGlobalGitHubSecret(); err != nil {
		a.warn("%s", err)
	}
	a.auditBindings(settings, settings.StageBindings())
	composition, err := config.Compose(settings, config.Selection{Gh: gh})
	if err != nil {
		return 0, err
	}
	if slices.Contains(composition.Agents, "copilot") {
		if ghLoggedIn {
			a.say("gh auth: ok (source of the per-box copilot secret)")
		} else {
			a.warn("gh is missing or not logged in; Copilot will be unauthenticated")
		}
	}
	if len(composition.AWSProfiles) > 0 {
		a.say("aws profiles (before books): %s", strings.Join(composition.AWSProfiles, " "))
		if composition.AWSDefaultProfile != "" {
			a.say("aws default profile (before books): %s", composition.AWSDefaultProfile)
		}
		if _, err := exec.LookPath("aws"); err != nil {
			a.warn("the aws cli is missing; the broker needs it")
		}
		a.brokerStatus(home)
	}
	return 0, nil
}

func (a *App) cmdUp(args []string, home string) (int, error) {
	fs := a.newFlags("up", "<repo> <slug> [--agent NAME]... [--book NAME|DIR]... [--aws-profile NAME] [--base B] [--cpus N] [--memory M] [--allow-global-policy] [--plan]")
	base := fs.String("base", "", "branch to start from (default: the repository's default branch)")
	var agentFlags, books stringList
	fs.Var(&agentFlags, "agent", "add an agent: "+strings.Join(config.AgentNames(home), ", ")+", or a kit reference (repeatable)")
	fs.Var(&books, "book", "use an installed book by name, or a book directory such as ./book (repeatable)")
	awsProfile := fs.String("aws-profile", "", "grant this AWS profile to the sandbox and make it the default")
	cpus := fs.intFlag("cpus", "CPUs for the sandbox")
	memory := fs.String("memory", "", "memory for the sandbox, such as 16g")
	allowGlobal := fs.Bool("allow-global-policy", false, "continue even if the global sbx policy has allow rules")
	approveRepoConfig := fs.Bool("approve-repo-config", false, "approve, and remember, the kits and books the repository's config asks for")
	plan := fs.Bool("plan", false, "write the env file and show sbx's plan without creating")
	positional, err := fs.parse(args, "repo", "slug")
	if err != nil {
		return 0, err
	}
	if _, err := sbx.RequireVersion(); err != nil {
		return 0, err
	}
	slug, err := checkSlug(positional[1])
	if err != nil {
		return 0, err
	}
	sandbox := sandboxOf(slug)
	repo := config.Resolve(config.ExpandUser(positional[0]))
	origin, err := source.Git(repo, "remote", "get-url", "origin")
	if err != nil {
		return 0, err
	}
	github, err := source.GitHubRepo(origin)
	if err != nil {
		return 0, err
	}
	settings, err := config.LoadSettings(home)
	if err != nil {
		return 0, err
	}
	auditExpected := settings.StageBindings()
	if *plan {
		defer func() { a.auditBindings(settings, auditExpected) }()
	}
	owner, _, _ := strings.Cut(github, "/")
	gh := a.FindGh()
	if err := a.checkGitHubAccess(settings, owner, gh); err != nil {
		return 0, err
	}
	if err := a.checkGitHubTokenForRepo(settings, github); err != nil {
		return 0, err
	}
	if err := checkNoGlobalGitHubSecret(); err != nil {
		return 0, err
	}

	if *allowGlobal && !settings.Managed.AllowsGlobalPolicy() {
		return 0, errorf("--allow-global-policy is refused by the managed settings (%s)", strings.Join(settings.Managed.Paths, ", "))
	}
	if !*plan {
		allows, err := sbx.GlobalAllows()
		if err != nil {
			return 0, err
		}
		if len(allows) > 0 && !*allowGlobal {
			return 0, errorf("the global sbx policy is not deny-all; run stage setup (or pass --allow-global-policy)")
		}
		if len(allows) > 0 {
			a.warn("continuing with global allow rules (--allow-global-policy)")
		}
		existing, err := sbx.Sandboxes()
		if err != nil {
			return 0, err
		}
		if existing[sandbox] {
			return 0, errorf("sandbox %s already exists", sandbox)
		}
	}

	if config.HasHidden(*base) {
		return 0, errorf("refusing --base %s: it has control or invisible characters", strconv.QuoteToASCII(*base))
	}
	directory := taskDir(home, slug)
	scratch := filepath.Join(directory, envfile.SourceDir)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return 0, err
	}
	branch, head, err := source.Prepare(repo, scratch, github, *base)
	if err != nil {
		return 0, err
	}
	if config.HasHidden(branch) {
		return 0, errorf("refusing to start from %s: the branch name has control or invisible characters", strconv.QuoteToASCII(branch))
	}
	a.say("source: %s %s at %s", github, branch, head[:min(8, len(head))])
	repoConfig, err := config.LoadRepoConfig(scratch)
	if err != nil {
		return 0, err
	}
	if *awsProfile != "" {
		if err := a.checkHostAWSProfile(*awsProfile); err != nil {
			return 0, err
		}
	}
	composition, err := config.Compose(settings, config.Selection{
		RepoNames:   []string{filepath.Base(repo), github},
		Agents:      agentFlags,
		Books:       books,
		Repo:        repoConfig,
		Gh:          gh,
		AWSProfile:  *awsProfile,
		GitHubOwner: owner,
	})
	if err != nil {
		return 0, err
	}
	config.AddStageBindings(auditExpected, composition.Bindings, "this sandbox")
	if err := a.checkRepoApprovals(settings.Home, composition, github, *approveRepoConfig, *plan); err != nil {
		return 0, err
	}
	for _, warning := range composition.Warnings {
		a.warn("%s", warning)
	}
	if len(composition.Agents) == 0 {
		a.warn("no agents chosen, so the sandbox has only a shell; use --agent, agents in config.toml or %s", config.RepoConfigPath)
	}

	carry, err := a.surveyGear(settings, config.Resolve(scratch))
	if err != nil {
		return 0, err
	}
	defer carry.Close()

	if len(composition.AWSProfiles) > 0 {
		tokenFile, err := broker.Grants{Path: grantsDir(home)}.Issue(slug, composition.AWSProfiles)
		if err != nil {
			return 0, err
		}
		composition.Secrets[config.BrokerService] = map[string]any{"command": "/bin/cat " + config.ShellQuote(tokenFile)}
	}
	if _, err := kits.Extract(home); err != nil {
		return 0, err
	}

	env := envfile.Build(composition, sandbox, directory, envfile.Options{CPUs: *cpus, Memory: *memory})
	envPath, err := envfile.Write(env, directory)
	if err != nil {
		return 0, err
	}
	task := Task{Slug: slug, Sandbox: sandbox, RepoDir: config.Resolve(scratch), GitHubRepo: github, SourceRepo: repo, Agents: composition.Agents, Takes: composition.Takes, BaseBranch: branch}
	_, recordErr := os.Lstat(filepath.Join(directory, "task.json"))
	if earlier, err := loadTask(home, slug); err == nil {
		task.Ships = earlier.Ships
	}
	if !*plan || os.IsNotExist(recordErr) {
		if err := writeTask(task, directory); err != nil {
			return 0, err
		}
	}

	if *plan {
		if err := sbx.Run("env", "plan", directory); err != nil {
			return 0, err
		}
		a.planGear(carry)
		a.say("plan only; env file %s", envPath)
		return 0, nil
	}
	if len(composition.AWSProfiles) > 0 && a.BrokerPID(home) == 0 {
		if err := a.BrokerStart(home); err != nil {
			return 0, err
		}
	}
	if err := sbx.Run("env", "create", "--force", "--no-input", directory); err != nil {
		return 0, errorf("%s; run `stage down %s` to clean up", err, slug)
	}
	if task.GitConfig, err = source.ConfigFingerprint(task.RepoDir, task.Sandbox); err != nil {
		return 0, err
	}
	if err := writeTask(task, directory); err != nil {
		return 0, err
	}
	if err := a.copyGear(carry, &task); err != nil {
		return 0, err
	}
	a.restoreLogins(settings, &task)
	a.say("%s is up: %s at %s", sandbox, github, task.RepoDir)
	var handles []string
	for _, kit := range composition.AllKits() {
		handles = append(handles, envfile.Handle(kit))
	}
	a.say("kits: %s", strings.Join(handles, ", "))
	return 0, nil
}

func (a *App) checkRepoApprovals(home string, composition *config.Composition, github string, approve, plan bool) error {
	cleared, err := config.ClearLegacyTrust(home)
	if err != nil {
		return err
	}
	if cleared {
		a.warn("stage now stores approvals as JSON; earlier approvals were cleared, and you'll be asked again")
	}
	approvals, err := config.RepoApprovals(composition, github)
	if err != nil {
		return err
	}
	missing, err := config.Unapproved(home, approvals)
	if err != nil || len(missing) == 0 {
		return err
	}
	printApprovals(a.Stderr, missing)
	if plan {
		return errorf("plan stopped before writing the env file: approve these first by running stage up without --plan, then plan again")
	}
	if !approve && !a.confirm("Approve these for this and future sandboxes while they are unchanged?") {
		return errorf("not approved; review them and answer y, or pass --approve-repo-config to approve them without asking")
	}
	if err := config.Approve(home, missing); err != nil {
		return err
	}
	for _, approval := range missing {
		a.say("approved %s (%s); a change to it asks again", config.Printable(approval.Label), approval.Fingerprint[:12])
	}
	return nil
}

func printApprovals(w io.Writer, missing []config.Approval) {
	fmt.Fprintf(w, "stage: the repository's %s asks for kits, books or takes you have not approved. They can widen the\n"+
		"  sandbox's network access, send credentials, run commands or copy files out of the sandbox. Review them before approving:\n", config.RepoConfigPath)
	for _, approval := range missing {
		fmt.Fprintf(w, "  - %s\n", config.Printable(approval.Label))
		for _, detail := range approval.Details {
			fmt.Fprintf(w, "      %s\n", config.Printable(detail))
		}
	}
}

const ghModeWarning = "[github] mode = \"gh\": sandboxes use your gh login. GraphQL lets them do anything that login can: " +
	"commit to and move or delete branches, open and merge pull requests, comment, change settings. The proxy refuses only " +
	"git push and REST writes. Use mode = \"fine-grained\" to keep sandboxes read-only"

func (a *App) checkGitHubAccess(settings *config.Settings, owner, gh string) error {
	if err := settings.CheckGitHub(owner, gh); err != nil {
		return err
	}
	if ignored := settings.IgnoredGitHubAppClientID(); ignored != "" {
		a.warn("%s", ignored)
	}
	if settings.GitHubMode() == config.GitHubApp {
		return a.checkGitHubApp(settings)
	}
	if settings.GitHubMode() != config.GitHubGh {
		return nil
	}
	if exec.Command(gh, "auth", "token").Run() != nil {
		return errorf("[github] mode = %q, but `gh auth token` fails; log in with `gh auth login`", config.GitHubGh)
	}
	a.warn("%s", ghModeWarning)
	return nil
}

func (a *App) reportGitHub(settings *config.Settings, gh string, ghLoggedIn bool) error {
	if ignored := settings.IgnoredGitHubAppClientID(); ignored != "" {
		a.warn("%s", ignored)
	}
	mode := settings.GitHubMode()
	if mode == "" {
		a.warn("%s", settings.CheckGitHub("", ""))
		return nil
	}
	if err := settings.Managed.CheckGitHubMode(mode); err != nil {
		a.warn("%s; stage up will refuse", err)
		return nil
	}
	switch mode {
	case config.GitHubNone:
		a.say("github: none (sandboxes get no GitHub credential)")
	case config.GitHubApp:
		a.reportGitHubApp(settings)
	case config.GitHubGh:
		if err := settings.CheckGitHub("", gh); err != nil {
			a.warn("%s; stage up will refuse", err)
		} else if !ghLoggedIn {
			a.warn("[github] mode = %q, but gh is missing or not logged in; stage up will refuse", config.GitHubGh)
		}
		a.warn("%s", ghModeWarning)
	case config.GitHubFineGrained:
		owners, err := settings.GitHubTokenOwners()
		if err != nil {
			return err
		}
		if len(owners) == 0 {
			a.warn("github: no fine-grained tokens in %s; save one per repository owner as <owner>.token (mode 600)", settings.GitHubTokenDir())
		}
		for _, owner := range owners {
			if err := settings.CheckGitHub(owner, ""); err != nil {
				a.warn("%s", err)
			} else {
				a.say("github token for %s: fine-grained. stage cannot see its permissions: keep it read-only (Contents and Metadata: read) and limited to your repositories", owner)
				a.reportGitHubTokenStatus(settings, owner)
			}
		}
		if _, err := os.Stat(settings.LegacyGitHubToken()); err == nil {
			a.warn("%s is no longer used; move it to %s for the owner it was created for", settings.LegacyGitHubToken(), settings.GitHubTokenFile("<owner>"))
		}
	}
	return nil
}

func checkNoGlobalGitHubSecret() error {
	services, err := sbx.GlobalSecrets()
	if err != nil {
		return err
	}
	if slices.Contains(services, config.GitHubService) {
		return errorf("sbx has a global %s secret, usually your full gh token; any sandbox with a %s binding, including one saved "+
			"by an earlier sandbox, can fall back to it. Remove it with `sbx secret rm %s -f`; sandboxes get GitHub access "+
			"only from [github] mode", config.GitHubService, config.GitHubService, config.GitHubService)
	}
	return nil
}

func writeTask(task Task, directory string) error {
	content, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	pending, err := os.CreateTemp(directory, ".task.json-")
	if err != nil {
		return err
	}
	defer os.Remove(pending.Name())
	_, writeErr := pending.Write(append(content, '\n'))
	if err := errors.Join(writeErr, pending.Chmod(0o644), pending.Sync(), pending.Close()); err != nil {
		return err
	}
	return os.Rename(pending.Name(), filepath.Join(directory, "task.json"))
}

func runInteractive(name string, args ...string) (int, error) {
	return runShowing(os.Stdout, name, args...)
}

func runShowing(stdout io.Writer, name string, args ...string) (int, error) {
	command := exec.Command(name, args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, stdout, os.Stderr
	err := command.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code > 0 {
			return code, nil
		}
		return 1, nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return 0, errorf("%s is not installed or not on PATH", name)
	}
	return 0, err
}

func (a *App) cmdShell(args []string, home string) (int, error) {
	fs := a.newFlags("shell", "<slug>")
	positional, err := fs.parse(args, "slug")
	if err != nil {
		return 0, err
	}
	task, err := loadTask(home, positional[0])
	if err != nil {
		return 0, err
	}
	return runInteractive("sbx", "exec", "-it", "-u", "agent", "-w", task.RepoDir, task.Sandbox, "bash", "-l")
}

func (a *App) cmdExec(args []string, home string) (int, error) {
	if len(args) > 0 && args[0] == "--detach" || len(args) > 1 && args[1] == "--detach" {
		return a.cmdDetachedExec(args, home)
	}

	if len(args) == 0 {
		return 0, &usageError{"the following arguments are required: slug"}
	}
	task, err := loadTask(home, args[0])
	if err != nil {
		return 0, err
	}
	command := args[1:]
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	if len(command) == 0 {
		return 0, errorf("usage: stage exec <slug> -- <command> [args...]")
	}
	argv := []string{"exec"}
	if a.IsTerminal() {
		argv = append(argv, "-it")
	}
	argv = append(argv, "-u", "agent", "-w", task.RepoDir, task.Sandbox)
	return runInteractive("sbx", append(argv, command...)...)
}

func PRBody(slug, sha, text, footer string) string {
	paragraphs := []string{fmt.Sprintf("This draft pull request was produced by an AI coding agent running in a scoring-stage sandbox "+
		"(task `%s`) and pushed from the host by `stage ship`.", slug)}
	if text = strings.TrimSpace(text); text != "" {
		paragraphs = append(paragraphs, text)
	}
	paragraphs = append(paragraphs, "Commit: "+sha)
	if footer = strings.TrimSpace(footer); footer != "" {
		paragraphs = append(paragraphs, footer)
	}
	return strings.Join(paragraphs, "\n\n") + "\n"
}

func (a *App) cmdShip(args []string, home string) (int, error) {
	fs := a.newFlags("ship", "<slug> [--ref R] [--branch B] [--pr] [--yes] [--allow-protected-paths]")
	ref := fs.String("ref", "HEAD", "sandbox ref to ship")
	branchFlag := fs.String("branch", "", "branch to push (default: <prefix><slug>/<sha>, with the [ship] branch_prefix, agent/ unless set)")
	pr := fs.Bool("pr", false, "open a draft pull request")
	yes := fs.Bool("yes", false, "push without asking, after reviewing the summary")
	allowProtected := fs.Bool("allow-protected-paths", false, "ship changes to CI, workflow or sandbox config files")
	positional, err := fs.parse(args, "slug")
	if err != nil {
		return 0, err
	}
	slug := positional[0]
	task, err := loadTask(home, slug)
	if err != nil {
		return 0, err
	}
	settings, err := config.LoadSettings(home)
	if err != nil {
		return 0, err
	}
	shipping := settings.Ship
	repo := task.RepoDir
	if err := checkGitConfig(task); err != nil {
		return 0, err
	}
	if task.GitConfig == "" {
		a.warn("task %s was created before stage recorded its git config; it cannot check that the scratch clone's config is unchanged", slug)
	}
	remote := "sandbox-" + task.Sandbox
	if _, err := source.Git(repo, "remote", "get-url", remote); err != nil {
		return 0, errorf("no %s remote in %s; is %s running?", remote, repo, task.Sandbox)
	}
	if _, err := source.Git(repo, "fetch", "--quiet", remote, *ref); err != nil {
		return 0, err
	}
	sha, err := source.Git(repo, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return 0, err
	}
	branch := *branchFlag
	if branch == "" {
		branch = fmt.Sprintf("%s%s/%s", shipping.BranchPrefix, slug, sha[:min(8, len(sha))])
	}
	if shipping.BranchPrefix == "" || !strings.HasPrefix(branch, shipping.BranchPrefix) || branch == shipping.BranchPrefix {
		return 0, errorf("refusing to push %s: branch must start with %s", printable(branch), shipping.BranchPrefix)
	}
	if config.HasHidden(branch) {
		return 0, errorf("refusing to push %s: the branch name has control or invisible characters", strconv.QuoteToASCII(branch))
	}
	if _, err := source.Git(repo, "check-ref-format", "refs/heads/"+branch); err != nil {
		return 0, err
	}
	if err := a.reviewShipment(repo, sha, branch, task.GitHubRepo, shipping, *yes, *allowProtected); err != nil {
		return 0, err
	}
	if err := source.GitInteractive(repo, "push", "https://github.com/"+task.GitHubRepo+".git", sha+":refs/heads/"+branch); err != nil {
		return 0, err
	}
	a.say("pushed %s to %s %s", sha[:min(8, len(sha))], task.GitHubRepo, branch)
	ship := Ship{SHA: sha, Branch: branch, GitHubRepo: task.GitHubRepo, At: time.Now().UTC().Format(time.RFC3339)}
	a.recordShip(home, slug, ship)
	if !*pr {
		return 0, nil
	}
	title, err := source.Git(repo, "log", "-1", "--format=%s", sha)
	if err != nil {
		return 0, err
	}
	var printed bytes.Buffer
	code, err := runShowing(io.MultiWriter(a.Stdout, &printed), "gh", "pr", "create", "--draft", "--repo", task.GitHubRepo, "--head", branch,
		"--title", title, "--body", PRBody(slug, sha, shipping.PRBody, shipping.PRFooter))
	if err == nil && code != 0 {
		return 0, exitCode(code)
	}
	if err != nil {
		return 0, err
	}
	url := pullRequestURL(printed.String(), task.GitHubRepo)
	if url == "" {
		a.warn("gh did not print the URL of a pull request in %s, so the ship is recorded without a pull request", task.GitHubRepo)
		return 0, nil
	}
	recorded := ship
	recorded.PullRequest = url
	a.recordShip(home, slug, recorded)
	return 0, nil
}

var pullRequestPattern = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/pull/[0-9]+$`)

func pullRequestURL(printed, github string) string {
	lines := strings.Split(strings.TrimSpace(printed), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	match := pullRequestPattern.FindStringSubmatch(last)
	if match == nil || !strings.EqualFold(match[1], github) {
		return ""
	}
	return last
}

func (a *App) recordShip(home, slug string, ship Ship) {
	err := func() error {
		task, err := loadTask(home, slug)
		if err != nil {
			return err
		}
		at := len(task.Ships)
		for i, known := range task.Ships {
			if known.SHA == ship.SHA && known.Branch == ship.Branch && known.At == ship.At {
				at = i
			}
		}
		task.Ships = append(task.Ships[:at:at], append([]Ship{ship}, task.Ships[min(at+1, len(task.Ships)):]...)...)
		return writeTask(*task, taskDir(home, slug))
	}()
	if err != nil {
		a.warn("the push is made, but stage could not record it on the task, so takes will not name it: %s", err)
	}
}

// protectedPaths are files whose changes run with the repository's secrets in
// CI or widen future sandboxes, so ship refuses them unless told otherwise.
var protectedPaths = []string{
	".github/", ".azure-pipelines/", ".pipelines/", ".circleci/", ".buildkite/", ".gitlab-ci.yml", "Jenkinsfile",
	".scoring-stage/", ".gitmodules",
	".devcontainer/", ".vscode/", ".husky/", ".pre-commit-config.yaml", "lefthook.yml", ".lefthook.yml", ".envrc",
	".mise.toml", "mise.toml", ".tool-versions",
}

func isProtected(path string, added []string) bool {
	base := filepath.Base(path)
	if strings.HasPrefix(base, "azure-pipelines") && (strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml")) {
		return true
	}
	for _, protected := range append(append([]string{}, protectedPaths...), added...) {
		if path == protected || (strings.HasSuffix(protected, "/") && strings.HasPrefix(path, protected)) {
			return true
		}
	}
	return false
}

func (a *App) reviewShipment(repo, sha, branch, github string, shipping config.ShipSettings, yes, allowProtected bool) error {
	base, err := source.Git(repo, "merge-base", "HEAD", sha)
	if err != nil {
		return err
	}
	if base == sha {
		return errorf("nothing to ship: %s has no commits beyond the sandbox's starting point", sha[:min(8, len(sha))])
	}
	commits, err := source.Git(repo, "log", "--no-decorate", "--format=%h %s", base+".."+sha)
	if err != nil {
		return err
	}
	stat, err := source.Git(repo, "diff", "--no-ext-diff", "--no-textconv", "--stat", base, sha)
	if err != nil {
		return err
	}
	names, err := source.Git(repo, "diff", "--no-ext-diff", "--no-renames", "--name-only", "-z", base, sha)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "stage: shipping to %s %s:\n%s\n%s\n", github, printable(branch), indent(printable(commits)), indent(printable(stat)))
	var protected []string
	for _, name := range strings.Split(names, "\x00") {
		if name != "" && isProtected(name, shipping.ProtectedPaths) {
			protected = append(protected, printable(name))
		}
	}
	if len(protected) > 0 {
		if !allowProtected {
			return errorf("refusing to ship changes to CI, workflow or sandbox config files: %s. These run with the "+
				"repository's secrets or widen future sandboxes; review them and pass --allow-protected-paths to ship them",
				strings.Join(protected, ", "))
		}
		a.warn("shipping changes to protected files: %s", strings.Join(protected, ", "))
	}
	if shipping.WarnMissingTrailer {
		unattributed, err := source.Git(repo, "log", "--no-decorate", "--format=%h", "--invert-grep", "-i", "--grep=^co-authored-by:", base+".."+sha)
		if err != nil {
			return err
		}
		if unattributed != "" {
			a.warn("commits without a Co-authored-by trailer naming the agent: %s", strings.Join(strings.Fields(unattributed), " "))
		}
	}
	if yes || a.confirm("Push these commits?") {
		return nil
	}
	return errorf("not shipped; rerun with --yes to push without asking")
}

func checkGitConfig(task *Task) error {
	if task.GitConfig == "" {
		return nil
	}
	current, err := source.ConfigFingerprint(task.RepoDir, task.Sandbox)
	if err != nil {
		return err
	}
	includes, _ := source.Git(task.RepoDir, "config", "--local", "--name-only", "--get-regexp", `^include(if\..*)?\.path$`)
	if includes != "" {
		return errorf("the git config in %s includes other files (%s); refusing to run git there", task.RepoDir, strings.Join(strings.Fields(includes), ", "))
	}
	if current != task.GitConfig {
		if whole, err := source.ConfigFingerprint(task.RepoDir, ""); err == nil && whole == task.GitConfig {
			return nil
		}
		return errorf("the git config in %s changed after stage up created the sandbox; refusing to run git there. "+
			"Inspect %s and recreate the task", task.RepoDir, filepath.Join(task.RepoDir, ".git"))
	}
	return nil
}

func printable(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = config.Printable(line)
	}
	return strings.Join(lines, "\n")
}

func indent(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

func (a *App) cmdList(args []string, home string) (int, error) {
	fs := a.newFlags("list", "")
	if _, err := fs.parse(args); err != nil {
		return 0, err
	}
	output, err := sbx.Output("ls")
	if err != nil {
		return 0, err
	}
	for index, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		if index == 0 || strings.HasPrefix(line, sandboxPrefix) {
			fmt.Fprintln(a.Stdout, line)
		}
	}
	return 0, nil
}

func (a *App) cmdDown(args []string, home string) (int, error) {
	fs := a.newFlags("down", "<slug> [--discard-takes]")
	discardTakes := fs.Bool("discard-takes", false, "remove the sandbox without copying its takes to the host")
	positional, err := fs.parse(args, "slug")
	if err != nil {
		return 0, err
	}
	slug, err := checkSlug(positional[0])
	if err != nil {
		return 0, err
	}
	directory := taskDir(home, slug)
	sandbox := sandboxOf(slug)
	existing, err := sbx.Sandboxes()
	if err != nil {
		return 0, err
	}
	task, taskErr := loadTask(home, slug)
	if _, recordErr := os.Lstat(filepath.Join(directory, "task.json")); taskErr != nil && !os.IsNotExist(recordErr) && existing[sandbox] && !*discardTakes {
		return 0, errorf("%s, so stage cannot tell which takes to keep; %s is left in place: pass --discard-takes to remove it without its takes", taskErr, sandbox)
	}
	if existing[sandbox] {
		jobs, err := listJobs(home, slug)
		if err != nil {
			return 0, err
		}
		var running []string
		for _, job := range jobs {
			if job.State == "running" {
				running = append(running, job.ID)
			}
		}
		if len(running) > 0 {
			return 0, errorf("%s has %d running jobs (%s); wait for them or stop them with stage job stop %s <id>; nothing was removed", sandbox, len(running), strings.Join(running, ", "), slug)
		}
	}
	if taskErr == nil && existing[sandbox] {
		if len(task.Takes) > 0 && !*discardTakes {
			if err := a.keepTakes(home, task); err != nil {
				return 0, errorf("%s; %s is left in place: fix that and run stage down again, or pass --discard-takes to remove it without its takes", err, sandbox)
			}
		}
		if settings, err := config.LoadSettings(home); err == nil && len(copiedAgents(settings, task)) > 0 {
			if err := a.saveLogins(settings, task); err != nil {
				a.warn("did not save logins: %s", err)
			}
		}
	}
	_, envErr := os.Stat(filepath.Join(directory, "sbxenv.yaml"))
	switch {
	case envErr == nil && existing[sandbox]:
		err = sbx.Run("env", "rm", "--force", "--no-input", directory)
	case existing[sandbox]:
		err = sbx.Run("rm", "--force", sandbox)
	}
	if err != nil {
		return 0, err
	}
	if err := (broker.Grants{Path: grantsDir(home)}).Revoke(slug); err != nil {
		return 0, err
	}
	if err := os.RemoveAll(directory); err != nil {
		return 0, err
	}
	a.say("%s removed", sandbox)
	return 0, nil
}

func copiedAgents(settings *config.Settings, task *Task) []string {
	var copied []string
	for _, agent := range task.Agents {
		if settings.CopiesLogin(agent) {
			copied = append(copied, agent)
		}
	}
	return copied
}

func (a *App) restoreLogins(settings *config.Settings, task *Task) {
agents:
	for _, agent := range copiedAgents(settings, task) {
		for _, loginFile := range settings.Agents.LoginFiles(agent) {
			file, err := os.Open(settings.SavedLogin(agent, loginFile))
			if err != nil {
				if loginFile.Required {
					a.say("no saved %s login yet: sign in with %s in `stage shell %s`, then run `stage login %s`", agent, agent, task.Slug, task.Slug)
					continue agents
				}
				continue
			}
			err = sbx.Feed(file, "exec", "-i", "-u", "agent", task.Sandbox, "sh", "-c",
				`umask 077 && mkdir -p "$(dirname "$1")" && cat > "$1"`, "sh", loginFile.Path)
			file.Close()
			if err != nil {
				a.warn("could not copy the saved %s login into %s: %s", agent, task.Sandbox, err)
				continue agents
			}
		}
		a.say("copied the saved %s login into %s", agent, task.Sandbox)
	}
}

func (a *App) saveLogins(settings *config.Settings, task *Task) error {
	for _, agent := range copiedAgents(settings, task) {
		for _, loginFile := range settings.Agents.LoginFiles(agent) {
			content, err := sbx.Output("exec", "-u", "agent", task.Sandbox, "cat", loginFile.Path)
			if err != nil || strings.TrimSpace(content) == "" {
				if loginFile.Required {
					return errorf("%s has no %s login yet; sign in with %s in `stage shell %s` first", task.Sandbox, agent, agent, task.Slug)
				}
				continue
			}
			if err := writePrivate(settings.SavedLogin(agent, loginFile), content); err != nil {
				return err
			}
		}
		a.say("saved the %s login to %s", agent, filepath.Join(settings.Home, "logins", agent))
	}
	return nil
}

func (a *App) cmdLogin(args []string, home string) (int, error) {
	fs := a.newFlags("login", "<slug>")
	positional, err := fs.parse(args, "slug")
	if err != nil {
		return 0, err
	}
	task, err := loadTask(home, positional[0])
	if err != nil {
		return 0, err
	}
	settings, err := config.LoadSettings(home)
	if err != nil {
		return 0, err
	}
	if len(copiedAgents(settings, task)) == 0 {
		return 0, errorf("%s has no agent listed in copy_logins in %s", task.Sandbox, filepath.Join(home, "config.toml"))
	}
	return 0, a.saveLogins(settings, task)
}

func writePrivate(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".login-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	_, writeErr := temporary.WriteString(content)
	if err := errors.Join(writeErr, temporary.Close()); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}
