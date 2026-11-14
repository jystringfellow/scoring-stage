# MCP uses the standard library for its tools-only stdio protocol

The stdio server needs newline-delimited JSON-RPC, initialization and version negotiation, tool schemas and
calls, ping and cancellation. The implementation uses Go's JSON, buffered I/O and context packages, with no new
dependency. A maintained MCP library would bring its own schema and transport abstractions and dependency
surface to audit; the tools-only wire handling here is a few hundred lines, separate from tool implementations.
The tradeoff is owning protocol validation and tracking revisions ourselves. Batches are refused and requests
and responses are bounded. Evidence: `TestMcpInitializeNegotiatesTheProtocolVersionAndDeclaresOnlyTools`,
`TestMcpMalformedAndUnknownRequestsGetProtocolErrorsAndTheServerKeepsServing`,
`TestMcpCancellingAWaitSendsNoResponseAndEofCancelsAnInflightWait`,
`TestMcpWritesOnlyProtocolToStdoutAndNeverLetsChildrenReadTheClientPipe`.
