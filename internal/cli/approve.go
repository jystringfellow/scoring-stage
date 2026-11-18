package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/envfile"
	"github.com/jystringfellow/scoring-stage/internal/source"
)

func (a *App) cmdApprove(args []string, home string) (int, error) {
	fs := a.newFlags("approve", "<repo> [--base B]")
	base := fs.String("base", "", "branch whose config to review (default: the repository's default branch)")
	positional, err := fs.parse(args, "repo")
	if err != nil {
		return 0, err
	}
	if a.approvalReadOnly || !a.IsTerminal() {
		return 0, errorf("stage approve records your consent, so it asks you in a terminal and has no flag that answers for you; run it yourself")
	}
	if config.HasHidden(*base) {
		return 0, errorf("refusing --base %s: it has control or invisible characters", strconv.QuoteToASCII(*base))
	}
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
	review, err := os.MkdirTemp("", "stage-approve-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(review)
	clone := filepath.Join(review, envfile.SourceDir)
	branch, head, err := source.Prepare(repo, clone, github, *base)
	if err != nil {
		return 0, err
	}
	if config.HasHidden(branch) {
		return 0, errorf("refusing to review %s: the branch name has control or invisible characters", strconv.QuoteToASCII(branch))
	}
	a.say("reviewing %s %s at %s", github, branch, head[:min(8, len(head))])
	repoConfig, err := config.LoadRepoConfig(clone)
	if err != nil {
		return 0, err
	}
	owner, _, _ := strings.Cut(github, "/")
	composition, err := config.Compose(settings, config.Selection{
		RepoNames:   []string{filepath.Base(repo), github},
		Repo:        repoConfig,
		Gh:          a.FindGh(),
		GitHubOwner: owner,
	})
	if err != nil {
		return 0, err
	}
	cleared, err := config.ClearLegacyTrust(home)
	if err != nil {
		return 0, err
	}
	if cleared {
		a.warn("stage now stores approvals as JSON; earlier approvals were cleared, and you'll be asked again")
	}
	approvals, err := config.RepoApprovals(composition, github)
	if err != nil {
		return 0, err
	}
	if len(approvals) == 0 {
		a.say("%s asks for nothing that needs approval on %s", github, branch)
		return 0, nil
	}
	missing, err := config.Unapproved(home, approvals)
	if err != nil {
		return 0, err
	}
	if len(missing) == 0 {
		a.say("%s is already approved as it is on %s; a change to its kits, books or takes asks again", github, branch)
		return 0, nil
	}
	printApprovals(a.Stderr, missing)
	if !a.confirm("Approve these for future sandboxes while they are unchanged?") {
		return 0, errorf("not approved; nothing was recorded")
	}
	if err := config.Approve(home, missing); err != nil {
		return 0, err
	}
	for _, approval := range missing {
		a.say("approved %s (%s); a change to it asks again", config.Printable(approval.Label), approval.Fingerprint[:12])
	}
	return 0, nil
}
