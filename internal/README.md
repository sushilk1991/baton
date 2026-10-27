# Internal packages

Add implementation packages here as their `TASKS.md` items begin. Keep
platform locks, process access, and pressure sampling behind internal
drivers. `cmd/baton` owns argument parsing and exit status; it does not
implement OS mechanics.
