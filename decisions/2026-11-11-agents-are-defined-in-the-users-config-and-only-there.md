# Agents are defined in the user's config, and only there

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
checks as other config, and cannot set `github` or `stage-broker`. So that every comparison of kits and services
agrees, a published kit reference with upper-case letters before its tag is refused wherever stage reads one, and
a service name that is not lower case and unpadded is refused in the user's config. Managed settings name agents, so a definition
must not change what a listed name means: with an agent list, a definition may set only `kit`, to another version
of a built-in agent's own kit; with a `copy_logins` list, no definition may set login files or move a listed name
to another kit. Rejected: letting a book define an agent, since a book could then supply a published kit and bind a
reserved service by calling it an agent.
Evidence: `TestAUserConfigDefinesAnAgentWithItsKitBindingsSecretAndLoginFiles`,
`TestADefinitionOfABuiltInNameOverridesItAndKeepsWhatItDoesNotSet`, `TestARepositoryCannotDefineAnAgent`,
`TestABookCannotDefineAnAgent`, `TestABookCannotClaimADefinedAgentsServicesOrHosts`,
`TestAgentDefinitionsAreDecodedStrictly`,
`TestUnderAnAgentPolicyADefinitionMayOnlyPinAnotherVersionOfABuiltInAgentsKit`,
`TestManagedCopyLoginsRefusesADefinitionThatChoosesTheFilesOrMovesAListedNameToAnotherKit`,
`TestAnAgentsDefaultSecretThatNeedsGhIsLeftOutWithoutGhAndYieldsToTheUsersSecret`.
