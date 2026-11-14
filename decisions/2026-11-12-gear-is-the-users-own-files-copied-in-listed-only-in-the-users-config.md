# Gear is the user's own files copied in, listed only in the user's config

Takes copy sandbox paths out and `copy_logins` copies a fixed set of login files in, but a user had no way to put
their own files, such as a tool's personal config or presets, into every sandbox. `gear` in the user's config lists
host files and directories that `stage up` copies into each new sandbox. `stage put` copies one host file into an
existing sandbox under the same source guard, managed refusal and destination rules, through the same tar stream.

- **The name.** stage names its own things after a session musician's work, and a player brings their own gear to
  the stage. Rejected: `props`, which belongs to a theatre stage; `cartage`, which is exact but needs explaining
  wherever it appears.
- **Only the user's config.** A repository's config is written by whoever can commit, a book is shared, and a
  `[[repos]]` rule is part of a book. Any of them listing gear would make stage hand the user's host files to an
  agent, so each is refused by name.
- **Managed settings.** `allow_gear = false` refuses it. Like every check on gear that depends on the machine, it
  is made by `stage up` and reported by `stage setup`, not when the config is loaded, so a refused entry cannot keep
  a user from shipping from, taking from or removing a sandbox they already have. Rejected for now: path patterns
  an administrator allows, which no one has asked for.
- **Credentials stay on the host.** Gear that is, is inside or holds stage's own directory or a known credential
  location is refused with no override, compared after following links and without regard to case, including where
  environment variables move those locations. The check fails closed: no home directory or an unreadable
  credential directory refuses all gear. It is a guard against mistakes, not a scan: stage does not read the files,
  and the agent can read everything listed. A destination on an agent's login files or in the workspace is refused,
  since `copy_logins` would save it as a login and `stage ship` would find it among the agent's work.
- **What is copied.** Regular files and directories. A listed path that is a link is followed, since the user named
  it; links and special files under a listed directory are never followed or copied. All gear together is bounded
  at 100 MiB and 10,000 entries, a tenth of a take's bounds because gear is configuration, and is measured before
  the sandbox is created. stage holds each source's directory open from that survey to the copy, so a path swapped
  in between cannot bring in a file from outside.
- **How.** sbx has no copy-in for an existing sandbox, so stage streams a tar through `sbx exec -i`, the path saved
  logins take, and treats a clean exit without the sandbox's unpacked line as a failure. Rejected: generating a kit
  whose `files/` hold the gear, which would put the user's files in the env file's kit list, and whose order
  against other kits' commands is unverified.
- **The user's copy wins, and a failed copy fails `up`.** Gear is unpacked over what a kit wrote, and before saved
  logins. A failed login copy only warns, because the agent can sign in again, but nothing would tell the user
  their config was missing.

Evidence: `TestOnlyTheUsersConfigMayListGear`, `TestGearCannotComeFromStagesOwnDirectoryOrAKnownCredentialLocation`,
`TestGearIsRefusedWhenTheHomeDirectoryIsUnknown`, `TestGearCannotLandOnAnAgentsLoginFilesOrInTheWorkspace`,
`TestManagedSettingsThatRefuseGearStopUpAndShowInSetupButLeaveDownWorking`,
`TestADirectorySwappedForALinkAfterTheSurveyCannotCarryAFileFromOutside`,
`TestUpFailsWhenSbxExitsCleanlyWithoutUnpackingTheGear`, `TestUpFailsAndNamesTheCleanupWhenGearCannotBeCopied` and
`TestPutAndGearRefuseTheSameSandboxPaths`.
