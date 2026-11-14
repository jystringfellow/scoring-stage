# Takes leave the sandbox as records, and down refuses to lose them

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

A take says what it belongs to. `stage down` deletes the task, so `stage ship` records each push on the task, with
the pull request when `--pr` made one, and every take carries a `stage.json` written by stage with the task's
identity and its ships. The name is reserved at a take's root so a sandbox cannot supply the record of its own
work. A push that cannot be recorded is still a push: ship warns and exits as the push did. The record is not a
complete log; what it can miss is listed under Limitations in the README. Evidence:
`TestAKeptTakeSaysWhichRepositoryBaseBranchAndShipsItBelongsTo`,
`TestAnEntryAtTheRootNamedLikeStagesManifestIsRefused` and
`TestShipWarnsAndExitsAsThePushDidWhenItCannotRecordIt`.
