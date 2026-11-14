# MCP offers names and existing sandbox operations, not the person's reserved choices

Tool schemas and handlers refuse approval, protected-path allowance, discarded takes, AWS profile grants,
global-policy overrides and resource overrides. Agents are configured names and books are installed names:
a kit reference or book directory could choose credential destinations or host commands. Approvals remain
read-only even when the CLI would migrate a legacy trust file. A missing approval returns its details and the
command for the person. Setup, broker control, tokens, logins and a host shell are absent.

A caller-chosen host file path could send host files to whatever a sandbox's agent can reach. MCP put therefore
also requires the resolved source to stay below the server's starting working directory, and refuses all puts
when that directory is the home directory or its ancestor. The CLI's credential guard remains a guard against
mistakes, not a scan. Cancellation cannot roll back uncancellable CLI paths; EOF drains them before restoring
process streams, rather than leaving handlers running against restored protocol streams. Waits and synchronous
exec are cancellable and time-capped; detached holders survive the server. Evidence:
`TestMcpBadArgumentsAndReservedInputsAreToolErrorsBeforeAnySbxCall`,
`TestMcpUpNeverApprovesAndReturnsWhatAndHowThePersonApproves`,
`TestMcpUpNeverRewritesLegacyApprovalsAndCanOnlyUseInstalledNames`,
`TestMcpPutSharesTheCommandChecksAndRestrictsSourcesToItsWorkingDirectory`,
`TestMcpExecTimeoutAndJobWaitAreCapped`, `TestMcpTakeAndDownKeepTakesAndRefuseFailedCopies`.
