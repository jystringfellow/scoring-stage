# Ship's conventions are user settings; its safety checks are not

`ship` carried one user's conventions as fixed behaviour: branches under `agent/`, a warning about commits without
a `Co-authored-by` trailer, and one pull request text. They are now `[ship]` settings in the user's config:
`branch_prefix` (default `agent/`), `warn_missing_trailer` (default off), `pr_body` and `pr_footer`, and
`protected_paths`. What stays fixed:

- A shipped branch always carries a prefix, and the prefix is a namespace: legal branch name components ending in
  `/`, checked when the config is loaded. The prefix is the only thing that keeps `ship` from pushing to a branch a
  person works on, so there is no way to turn it off, and a prefix such as `ma`, which `main` starts with, is
  refused. The rules are git's, written in Go so that loading a config runs nothing; a test holds them to
  `git check-ref-format`. Every other check on a branch name is unchanged.
- Only the user's config sets `[ship]`. A repository's config is written by whoever can commit, including the agent
  whose work is being shipped, and a book is shared, so neither may choose where pushes go or what is protected.
- `protected_paths` only adds. stage's own list is the floor; `--allow-protected-paths` remains the one way past
  it, per ship. An entry that could never match as written, such as a pattern or `./deploy/`, is refused instead of
  being left to protect nothing, and a rename counts against the path it moves from.
- A `--pr` body always opens with a sentence stage writes, saying an AI coding agent in a scoring-stage sandbox
  produced the change, and always names the commit. `pr_body` replaces only the paragraph after that sentence.
  Rejected: letting `pr_body` replace everything and checking that it still discloses. No check on free text can
  tell a disclosure from a sentence that contains the word "AI", in any language, so a fixed sentence is the only
  form that holds whatever is configured. For the same reason `pr_body` and `pr_footer` cannot hold invisible
  characters or open an HTML comment, which would hide the commit line from the rendered pull request.

Evidence: `TestShipPushesUnderTheConfiguredBranchPrefixAndRefusesBranchesOutsideIt`,
`TestAShipBranchPrefixThatIsEmptyOrHasHiddenCharactersIsRefused`, `TestOnlyTheUserConfigSetsShipSettings`,
`TestShipRefusesPathsTheUserConfigAddsToTheProtectedList`,
`TestPRBodySaysAnAIAgentProducedTheChangeWhateverBodyIsConfigured`,
`TestShipSaysNothingAboutTrailersByDefault`,
`TestAShipBranchPrefixIsANamespaceOfLegalRefComponentsEndingInASlash`,
`TestEveryShipBranchPrefixStageAcceptsGitAcceptsAsTheStartOfABranch`,
`TestAddedProtectedPathsThatCouldNeverMatchAsWrittenAreRefused`,
`TestShipRefusesARenameThatMovesAProtectedFileAway`,
`TestPRBodyAndFooterRefuseHiddenCharactersAndHTMLCommentOpeners`.
