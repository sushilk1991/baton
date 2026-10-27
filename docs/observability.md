# Status and history

```sh
baton status
baton status --json
baton status --line
baton status --watch
```

Status shows lanes, used/current/configured capacity, running jobs, ordered
waiters, RAM, swap, and pressure. Running rows include label, agent, repo,
runtime, RSS, and PID. Queued rows include position, wait, priority, and
weight. Slot usage comes from kernel locks, not stale PID files.

`--line` is suitable for a tmux, starship, or Claude statusline command. It
prints lane occupancy, waiting count, and memory pressure. Status does not
modify a shell or agent's statusline configuration.

`--watch` appends one snapshot per second until interrupted. Combine it
with `--json` for JSONL or `--line` for compact lines; `--json` and `--line`
are mutually exclusive. Text watch does not clear the screen. Configuration
is loaded once; restart after edits. Probe errors return a nonzero exit.

Snapshots are observations across several files and counters, not atomic
transactions. A job can finish while you read. RSS is the runner's latest
sample and can miss short-lived descendants. Windows swap is unavailable,
not inferred from page-file commit counters.

## Completed jobs

```sh
baton history --since 24h --lane render
baton history --json
```

History records start/end times, wait, exit code, peak RSS, lane, label,
agent, repo, and cache-hit status. JSONL also carries job identity, user,
cwd, signature, and declared or learned memory. Durations are nanoseconds;
memory is bytes; timestamps are UTC. Raw argv is not persisted. Avoid
putting secrets in labels or paths.

`--since` is a nonnegative Go duration filtering by completion time;
`--lane` is an exact match. No filter means all retained records. Empty
history produces no JSON lines. Malformed records fail explicitly even
when a filter would exclude them.

The history log rotates at 10 MiB and retains one previous segment. It is
local operational history, not durable audit storage. Persistence errors
are reported on stderr without changing the child's exit code. `BATON=off`
runs have no history record. [Architecture](architecture.md) explains
sampling, command signatures, and learned-memory updates.
