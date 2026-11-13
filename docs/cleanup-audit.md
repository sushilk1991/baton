# Cleanup audit, 2026-11-13

Each iteration is a separate commit. SPEC.md is unchanged.

## Process-tree RSS

Index PID-to-RSS once instead of scanning the process list for every
descendant. Traversal remains cycle-safe, saturates on overflow, and keeps
the first sample for duplicate PIDs. Tests also cover a missing root.

Native Go 1.27 benchmarks, three samples per size (median):

| Processes in a chain | Before | After | Allocated before | After |
| --- | ---: | ---: | ---: | ---: |
| 1,000 | 0.483 ms | 0.204 ms | 258 KB | 201 KB |
| 10,000 | 17.05 ms | 2.66 ms | 2,139 KB | 1,623 KB |

Host load varied; these measure tree summation, not total command latency.
The traversal now costs O(processes + descendants), avoiding the nested
scan. Input collection and sampling frequency are unchanged.

Passed: native and Linux `make check`, including race tests and all five
cross-builds; golangci-lint v2.15.0 reports 0 issues. Test homes and every
XDG path are isolated. Linux uses a source snapshot copied from a read-only
mount into the container; ignored historical artifacts are excluded.

The first Linux harness left test temp files on a macOS bind mount, causing
shim permission failures. Moving them into the container resolved those.
A later full run hit TestTwentyJobs's existing 15-second deadline. Three
isolated repetitions passed with each sampler: baseline 5.40/13.33/7.20 s,
new 5.17/14.76/8.94 s. The final full Linux gate passed unchanged; the
deadline failure's exact cause remains unknown. No check was weakened.

Raw logs and the isolation harness: `/tmp/baton-audit-SQCacg/rss-*.log` and
`/tmp/baton-audit-SQCacg/isolated.py` (temporary local artifacts).

## MCP handshake

Replace `initialized, ready bool` with one private phase. The state
`initialized=false, ready=true` has no corresponding phase.

```text
 NEW -- initialize --> INITIALIZED -- notification --> READY
  |                         |                           |
  +---- tools rejected -----+                     tools allowed
```

Wire-level ordering tests pass both before and after the refactor: early
ping, premature/repeated notifications, invalid initialization, tool calls
between handshake steps, and duplicate initialization. Native and Linux
`make check` pass; pinned lint reports 0 issues. Logs: `mcp-*.log` in the
same temporary artifact directory.
