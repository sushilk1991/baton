# Changelog

## Unreleased

### Scheduling and observation

- Weighted, crash-released lane locks; priority/FIFO tickets with aging.
- Memory pressure admission on macOS/Linux and best-effort Windows,
  hysteresis, memory-wait timeout, and learned process-tree RSS.
- Foreground execution, Unix process-group signals, exit-code passthrough,
  live status, and rotated JSONL history.

### Integration and maintenance

- Hardware-derived TOML policy, XDG paths, command shims, and bypass rules.
- Orphan discovery/termination and rooted intermediate-file collection.
- Single-file output cache with restoration and size-based eviction.
- Shared headless-browser supervisor with CDP connection counting.
- Reversible agent setup, optional Claude Bash hooks, and read-only MCP.

### Documentation and validation

- Source installation, quick start, recipes, generated defaults, and a VHS
  demo tape; contribution, conduct, and security-reporting guidance.
- Native and Linux process/race checks and five cgo-free cross-build targets.
  Cross-builds do not establish Windows runtime behavior. Hosted CI results,
  release distribution, stress benchmarks, and recorded demo remain pending.
