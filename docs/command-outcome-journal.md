# Durable command outcomes

Every command started by the AgentDock runtime has a private record under
`AGENTDOCK_HOME/commands-v1/`. Before spawning a process, AgentDock atomically
writes its session/event identity, execution context revisions, argument digest,
and `starting` state. It never journals raw command arguments or environment
values. Command output itself may contain sensitive data; directory/file access
is restricted to the AgentDock OS user.

Output is retained as an independent tail of at most 64 KiB per stream. Each
output write updates the record independently of the live MCP observation cursor.
The terminal state, output tail and `pending_report` flag are committed together
using a synced atomic replacement. A failed persistence operation is surfaced and
retried by later reads; an uncommitted outcome is never reported as durable.

Restart recovery preserves completed outcomes. Records still marked `starting`
or `running` become `outcome_unknown` with no exit code, even if the process may
have finished before the crash. They are never rerun. The runtime owns an OS lock
on the journal for its lifetime; a second runtime cannot recover a live runtime's
commands. OS locks release automatically on process exit.

`exec_command.request_id` is an optional stable idempotency key scoped to the
Project WorkSession/Target (or the standalone node). Reusing it with the same
execution arguments returns the original session; changing arguments or context
revisions fails with `COMMAND_REQUEST_CONFLICT`. Response preferences such as
yield time, execution mode and output limit do not affect the digest. No implicit
transport request ID is used. An omitted request ID starts a new command.

The optional Bridge capability `bridge.command.outcomes.v1` enables
`command.outcomes.read` and `command.outcomes.ack`. Only Project-bound records
cross the authenticated outbound Bridge. Pending reads select unacknowledged
terminal outcomes; explicit session IDs also return running and already-ACKed
records, so a later `await` can retrieve a command that finished earlier. Reads
are bounded by the shared protocol limit and a 1 MiB response budget. Each Bridge
outcome includes at most 32 KiB total stdout/stderr; dropped-byte counters disclose
truncation. ACK clears only the pending flag, preserving all execution facts.

The initial retention policy has **no automatic TTL or eviction**. Both pending
and acknowledged records, outputs and idempotency keys are retained together up
to 4,096 records per node. At capacity, new commands fail with
`COMMAND_JOURNAL_FULL`; querying, ACKing, and replaying existing request IDs remain
available. This backpressure avoids silently forgetting an old idempotency key
and executing it again. Operators must archive state deliberately; deleting the
journal removes its idempotency history. Automatic archival/tombstones are a
separate future change.

This persists execution facts; it does not keep child processes alive across
AgentDock restarts and does not guarantee exactly-once external side effects.
NexusDock alone owns continuation/Wake policy, and only an explicit await creates
continuation eligibility.
