# Tasks

One item per iteration, in order. Check it off only when its tests pass.

- [x] Scaffold: go.mod (`github.com/sushilk1991/baton`, go 1.22), `cmd/baton`, `internal/` layout, Makefile with `check`, `.golangci.yml`, CI workflow, LICENSE (Apache-2.0), .gitignore
- [x] Lanes and slots: lock-file semaphores (flock on Unix, LockFileEx on Windows), weights, crash safety; tests a and b
- [x] Queue: tickets, FIFO within priority, aging, dead-ticket cleanup; test c
- [x] Pressure providers: darwin, linux (PSI, meminfo, cgroup v2), windows; gate with hysteresis and max_wait; fake provider; test e
- [x] `baton run`: exec, process group, signal forwarding, exit codes, wait notices, auto lane, identity detection, history JSONL, peak-RSS sampling, learned memory; test d
- [x] Config: TOML, hardware-derived defaults, `config init|show|path|validate`, XDG paths on every OS
- [x] `status` (`--json`, `--watch`, `--line`) and `history`
- [x] `shim install|uninstall|list` with fast paths and recursion guard; test f
- [x] `reap`; test g
- [x] `gc`; test h
- [x] Cache (`--cache-key`, `--output`, `cache ls|prune`); test i
- [x] `browser start|stop|status|endpoint` with idle shutdown and client counting
- [ ] `init` (skill, AGENTS.md/CLAUDE.md snippet, optional Claude Code hook, manifest, `--uninstall`) and `mcp`; test j
- [ ] Docs: README, docs/*, demo.tape, comparison table, community files
- [ ] Distribution: .goreleaser.yaml, install.sh, Homebrew formula template, release workflow (do not publish)
- [ ] `scripts/stress.sh` and `docs/benchmarks.md` with real results from this machine
- [ ] Final gate: `make check` on macOS, `make check` in `golang:1.27` Docker, every README command re-run
