# One holder per job, with file-lock liveness

An attached host sbx client is the available way to keep a sandbox running for an unattended command.
Each detached job gets one host holder, with no shared daemon or listener, and private output and an atomic
result under its task. A file lock inherited from the starter proves liveness; no pid decides state or is
used to stop a job. A missing result and a released lock mean lost, including after a host restart, because
stage cannot prove what happened to the sandbox's command. Evidence:
`TestAnExecCallerCanExitBeforeItsDetachedCommand`, `TestAJobWhoseHolderWasKilledIsReportedLost`,
`TestAJobLeftRunningByAHostThatRestartedIsReportedLost`, `TestJobReadersNeverMistakeAnotherReadersLockForAHolder`.

Down deletes the task and its job output, so it refuses while a job is running; stop is explicit and keeps its
output, and finished and lost jobs do not block down. Wait returns a command's status, 124 for a timeout while
running, and 125 for lost or stopped; JSON state resolves a command that itself exits 124 or 125. Evidence:
`TestDownRefusesWhileAJobIsRunningAndRemovesFinishedJobsWithTheTask` and
`TestWaitReturnsTheJobsStatusAndHonoursItsTimeout`.
