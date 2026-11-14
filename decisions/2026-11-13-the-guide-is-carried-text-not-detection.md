# The guide is carried text, not detection

`stage init --guide` prints a procedure instead of inspecting repositories: detection would make stage own a
catalogue of toolchains, which belong in books and kits. One embedded text file supplies the binary's output;
a test keeps the README's verbatim copy equal to it. The optional skill runs the guide to avoid another copy
and requires the agent to propose and never approve. Evidence:
`TestInitGuidePrintsTheProcedureInOrderAndNothingElse`, `TestInitGuideWritesNothingAndCallsNoSbx`,
`TestTheReadmeCopyOfTheGuideIsTheGuide`,
`TestTheSkillCarriesNoCopyOfTheGuideAndTellsTheAgentToRunItAndNeverApprove`.
