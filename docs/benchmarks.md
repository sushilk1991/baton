# Benchmarks

No machine stress results have been recorded. Swap, pressure, OOM/jetsam,
and throughput targets remain unverified. Unit and process tests are
correctness checks; they are not evidence of reduced machine pressure.

The queued stress task will add `scripts/stress.sh`, run synthetic jobs
with and without baton on this machine, and record the real output here.
It must capture peak swap, worst pressure, kills, and wall time. Passing
requires pressure below critical, swap growth below 1 GiB, no kills, and
wall time no more than 1.2 times sequential execution.

Record the OS, RAM, CPU count, job count, per-job allocation, durations,
configuration, and raw command output with each result. Do not extrapolate
one machine's result to arbitrary workloads or hardware.
