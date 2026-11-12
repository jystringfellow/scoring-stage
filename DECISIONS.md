# Decisions

Decisions that span many changes. Add an entry when a choice constrains future work; reverse one only with new
evidence, and add the reversal as a new entry. What stage owns today is in the ownership table in
[docs/design.md](docs/design.md); live evidence is in [VERIFICATION.md](VERIFICATION.md).

## 2026-11-08: Name scoring-stage, binary stage, and call packs books

A scoring stage is where session players record, and a book is the folder of charts a player is handed for a gig.
"Packs" became books. Rejected: rigs, because "rig" is OpenRig's word for a group of agents, which kapellmeister
imitates with ensembles and chairs, and a stage is where an ensemble runs, not a rig; charts (Helm); stagings and
layouts.

## 2026-11-08: One Go binary with embedded kits

stage installs as one binary, like sbx. sbx accepts local kits only as directories, so the core kits are embedded
and extracted to a content-addressed directory that a new build never rewrites. The AWS broker is the same binary.

## 2026-11-08: Refuse pushes on ports 443 and 80 only

A bounded deny makes sbx require HTTP inspection, and sbx fails closed on connections it cannot inspect. A push
deny on `**` refused every non-HTTP connection, such as a database on 1433, so it is limited to `**:443` and
`**:80`. SSH or another port to a git host is a push path the proxy does not refuse; that is a documented
limitation. Evidence: VERIFICATION.md, 2026-11-08 to 2026-11-09 entry.

## 2026-11-08: Agents sign in per sandbox; copy_logins is opt-in

Credentials stay on the host wherever the proxy can hold them. agy's sign-in is a token file inside the sandbox, so
by default agy signs in per sandbox. `copy_logins` copies a saved login in, only when the user's own config asks.
Evidence: `TestLoginsAreLeftAloneUnlessCopyLoginsAsksForThem`.

## 2026-11-08: A default AWS profile, and no --aws-region

`[aws] default_profile` is written as `[default]` and must be a granted profile; one granted profile becomes the
default. `stage up --aws-profile` grants one host profile to one sandbox. There is no `--aws-region`: a sandbox has
one region for every profile, so a global override would move a book's profiles too. Evidence:
`TestAWSDefaultProfileMustBeGranted`, `TestTheOnlyGrantedAWSProfileBecomesTheDefault`,
`TestTheAWSProfileFlagIsGrantedAndOverridesEveryOtherDefault`.

## 2026-11-09: The broker serves only temporary AWS keys

The broker answers only with credentials that carry a session token and expire within about 12 hours, so an IAM
user's long-lived keys never reach a sandbox. The keys a sandbox gets stay usable until they expire. Evidence:
`TestOnlyTemporaryCredentialsThatExpireSoonAreServed`.

## 2026-11-09: GitHub access is a required choice

`[github] mode` is fine-grained, app, gh or none, with no default, because each trades access against safety
differently. gh mode is allowed with a warning, since GraphQL lets it write. stage ships no GitHub App; users
register their own. Under managed settings, app and gh are refused unless `allowed_modes` lists them. Evidence:
`TestUpRefusesUntilAGitHubModeIsChosen`, `TestManagedSettingsWithoutAllowedModesRefuseOnlyGhMode` (gh),
`TestManagedSettingsRefuseAppModeUntilAllowedModesListsIt` (app).

## 2026-11-09: Kits, not bindings, decide where a credential goes

A live probe showed a kit's `credential@1` inject domain decides where a secret is sent, and a binding only
authorises the service. So books and repository configs may use only local kits, which stage checks; a book's kits
may declare only credentials the book supplies, and a repository's none; no book or repository may claim a reserved
service. The user's own config may still name published kits. Evidence: VERIFICATION.md, 2026-11-09 credential
injection entry; `TestBooksCannotNamePublishedKits`, `TestARepositoryKitIsADotPathNeverAPublishedKit`,
`TestABookKitCannotDeclareAReservedCredential`, `TestUserConfigMayStillNamePublishedKits`.

## 2026-11-09: Never prune bindings

`sbx env rm --prune-bindings` removes a service's whole shared binding for every sandbox on the machine. `stage
down` never passes it. Evidence: VERIFICATION.md, 2026-11-09 credential injection entry;
`TestStageNeverPrunesTheSharedBindings`.

## 2026-11-09: Create sandboxes unattended; report sbx's bindings, do not gate on them

`sbx env create --force --no-input` keeps unattended runs possible; stage asks its own questions (repository
approvals, ship review) instead of sbx's prompts. sbx's `credentials.yaml` is sbx's and undocumented, so `stage
setup` and `stage up --plan` only report what it holds and how to remove it. A parse failure is a warning.
Evidence: `TestSetupStillAuditsBindingsWhenGlobalSecretsAreUnreadable`, `TestUpPlanAuditsGlobalBindingsEvenWhenThePlanFails`.

## 2026-11-09: Approve repository kits and books by content; installed books are not yet approved

A repository's kits and the books its config turns on are approved by content fingerprint. Books the user turns on
are not reviewed as of this entry; the fence above stops a book from rebinding stage's credentials, but a book's secret
commands are still code on the host. Pinning installed books by content is planned. Until then, turn on books only
from protected, reviewed repositories. Evidence: `TestPlanStopsBeforeWritingTheEnvFileWhenApprovalsAreMissing`,
`TestALegacyTrustFileIsClearedRatherThanMigrated`.

## 2026-11-09: Tests are the spec; live checks go in VERIFICATION.md

Behavior lives in tests, reasons in commits and PRs, and decisions that span changes here. `go test` runs against
fakes, so what has been checked against a live sbx or GitHub is recorded separately, as dated entries that are never
rewritten.

## 2026-11-10: Keep the public repository generic

This repository and its history are public. Names and details of the organisations that use stage stay out of
code, tests, docs, commits, PRs, reviews and branch names. Tests use neutral names. Evidence: AGENTS.md, Keep it
generic.

## 2026-11-10: stage is a thin wrapper

Use an sbx primitive wherever one exists, shape any seam stage owns like the primitive it stands in for, and
propose the gap upstream. What one organisation or team needs belongs in a book. Evidence: AGENTS.md, Constraints.

## 2026-11-10: ship's config check leaves out the remote sbx manages

sbx removes the `sandbox-<sandbox>` remote from the scratch clone when the sandbox stops and adds it on a new port
when the sandbox starts, so a hash that included it refused every sandbox that had restarted. The hash leaves the
remote out only while it has exactly sbx's shape: a loopback `git://` URL on any port, path `/src`, and sbx's two
fetch refspecs. Any other key in that section, another host, transport or path, or a second copy is still hashed.
A task recorded before this, with a hash of the whole config, still ships while its config is byte-identical.
Evidence: TestConfigFingerprintIgnoresTheRemoteSbxAddsOnEachStart,
TestConfigFingerprintKeepsASandboxRemoteThatSbxDidNotWrite,
TestShipAllowsSbxToMoveTheSandboxRemoteWhenTheBoxRestarts,
TestShipAcceptsATaskRecordedWithTheWholeConfigWhileItIsUnchanged.

## 2026-11-10: Takes leave the sandbox as records, and down refuses to lose them

A sandbox's own records, such as an orchestrator's run telemetry, were deleted with it. A take is a sandbox path
that stage copies to `~/.scoring-stage/takes/<slug>/<time>/` on `stage take` and before `stage down`. Config, books
and repositories may list takes; a repository's need approval, since they choose what leaves the sandbox, and the
approval is for that list in that repository, so the same list elsewhere asks again. The copy
is a tar stream through `sbx exec`, written by stage itself: regular files and directories only, private modes,
nothing outside the take's directory, bounded in size, count and time, and visible only once complete. `down`
leaves the sandbox in place when the copy fails or finds only links, unless `--discard-takes` is passed, so a record
is never lost silently. Takes
only flow out: nothing stage keeps is copied into a later sandbox, which is why they need no review step. Rejected:
a host directory mounted read-write into every sandbox, which would let one sandbox's output reach the next.
Evidence: `TestDownKeepsTheTakesOnTheHostBeforeRemovingTheSandbox`,
`TestDownLeavesTheSandboxWhenItsTakesCannotBeCopiedUnlessToldToDiscardThem`,
`TestAnEntryThatWouldLandOutsideTheDirectoryIsRefused`, `TestLinksAndSpecialFilesAreLeftBehindAndCounted`,
`TestARepositorysTakesAreUsedOnlyAfterApproval`, `TestATaskKeepsTheTakesItWasCreatedWithWhenTheConfigChanges`,
`TestWhatTheSandboxSendsIsBoundedEvenWhenNoneOfItIsKept`, `TestStreamStopsAnSbxThatDoesNotFinishInTime`,
`TestDownLeavesTheSandboxWhenItsTakesHoldOnlyLinks`.

## 2026-11-11: A take says what it belongs to

`stage down` deletes the task, so a kept take could not say which repository, base branch, shipped branch or pull
request it came from. `stage ship` now records each push on the task, with the pull request when `--pr` made one,
and every take carries a `stage.json` written by stage with the task's identity and its ships. The name is reserved
at a take's root so a sandbox cannot supply the record of its own work. A push that cannot be recorded is still a
push: ship warns and exits as the push did. Because ship now rewrites the task's record, the record is replaced
whole and never written into, `down` leaves a sandbox whose record cannot be read unless told to discard its takes,
and `up --plan` leaves a live task's record alone. A pull request is recorded only when it is in the task's own
repository, and branch names with control or invisible characters are refused before they are recorded. Evidence: `TestShipRecordsEachPushOnTheTask`,
`TestShipWithPRRecordsThePullRequestURLGhPrints`, `TestShipWarnsAndExitsAsThePushDidWhenItCannotRecordIt`,
`TestAKeptTakeSaysWhichRepositoryBaseBranchAndShipsItBelongsTo`,
`TestAnEntryAtTheRootNamedLikeStagesManifestIsRefused`,
`TestATaskRecordedBeforeBaseBranchAndShipsStillShipsAndTakes`,
`TestShipReplacesTheTaskRecordWholeAndNeverWritesIntoTheOldOne`,
`TestDownLeavesTheSandboxWhenItsTaskRecordCannotBeReadUnlessToldToDiscardItsTakes`,
`TestUpPlanLeavesALiveTasksRecordAsItWas`, `TestShipRecordsOnlyAPullRequestInTheTasksOwnRepository`,
`TestShipRefusesABranchNameWithHiddenCharacters`, `TestUpRefusesABaseBranchNameWithHiddenCharacters`.

## 2026-11-11: The ship record is kept across another ship and another up, and is not a complete log

A review found a ship could drop another's record: ship read the task once, then wrote that copy back after the
review prompt, the push and `gh pr create`. Ship now reads the record again immediately before each write and
changes only its own entry, which leaves a gap of milliseconds, not minutes; there is no lock. `stage up` on a slug
whose sandbox is gone keeps the ships already recorded. `down` also leaves a sandbox whose record exists but cannot
be opened or does not name the task. A sandbox with no record at all is still removed, since stage made nothing
there to keep. Branch names are held to a stricter rule than other text: any Unicode format character, line or
paragraph separator, filler or invalid UTF-8 is refused, because a branch name is recorded and shown as the proof
of what was pushed. The record is written to disk before it replaces the old one. What the record can still miss
is listed under Limitations in the README. Evidence:
`TestShipKeepsAShipRecordedByAnotherShipWhileItWaitedForThePullRequest`,
`TestUpAgainOnASlugWhoseSandboxIsGoneKeepsTheShipsAlreadyRecorded`,
`TestDownLeavesTheSandboxWhenItsTaskRecordCannotBeReadUnlessToldToDiscardItsTakes`,
`TestShipRefusesABranchNameWithHiddenCharacters`, `TestHiddenCharactersAreFoundWhereControlCharactersAreNot`.

## 2026-11-11: Agents are defined in the user's config, and only there

The agent table was compiled in: four agents, each with a pinned mixin, its bindings and its login files. Any other
mixin worked only as a bare kit reference, and a new mixin version needed a release. The table is now built-in
defaults plus `[agent.<name>]` definitions in the user's config (kit, bindings, default secret, login files), and a
definition of a built-in name replaces what it sets and keeps the rest. Copilot's secret from `gh auth token` moved
into its own definition as a default secret with a `{gh}` placeholder, so no generic code names an agent.

Only the user's config may define an agent. A definition picks a kit, which decides where credentials go; a secret
command, which runs on the host; and login files, which are credentials copied into sandboxes. Those are the things
a book may do only inside the fence and a repository not at all, so a book or repository with an `agent` table is
refused and both still only name agents. The services a defined agent binds or supplies, and its bindings' hosts,
are reserved against books and repository kits exactly as a built-in agent's are. A definition is held to the same
checks as other config, and cannot set `github` or `stage-broker`. Managed settings name agents, so a definition
must not change what a listed name means: with an agent list, only a built-in name kept on its own kit at another
version is accepted; with a `copy_logins` list, no definition may set login files. Rejected: letting a book define
an agent, since a book could then supply a published kit and bind a reserved service by calling it an agent.
Evidence: `TestAUserConfigDefinesAnAgentWithItsKitBindingsSecretAndLoginFiles`,
`TestADefinitionOfABuiltInNameOverridesItAndKeepsWhatItDoesNotSet`, `TestARepositoryCannotDefineAnAgent`,
`TestABookCannotDefineAnAgent`, `TestABookCannotClaimADefinedAgentsServicesOrHosts`,
`TestAgentDefinitionsAreDecodedStrictly`, `TestAgentPolicyRefusesADefinitionThatChangesWhichKitANameMeans`,
`TestManagedCopyLoginsRefusesLoginFilesFromADefinition`,
`TestAnAgentsDefaultSecretThatNeedsGhIsLeftOutWithoutGhAndYieldsToTheUsersSecret`.
