# Progress

Shipped locally: thirteen of seventeen TASKS.md items, through init and MCP.
Implementation: d3ce3ad. SPEC.md is unchanged. No push, tag, release, or
remote change. The overall goal remains incomplete.

Verified: native `make check` and
`docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` exited 0
with the final code. Both passed vet, race tests, and all five cgo-free
cross-builds. golangci-lint was unavailable and skipped. Windows compiled;
its runtime remains unverified.

`init` previews by default and applies with --yes; --dry-run always prevents
writes. It installs the shared Agent Skills document, links detected agent
skill directories, and appends marked instructions to supported global
files. Aider uses its read-only config field. Optional Claude hooks either
rewrite heavy Bash calls or block with the exact queued command.

A private write-ahead manifest records original bytes and modes, installed
bytes, links, and created directories. Reinstall resumes interrupted
publication. Uninstall restores exact originals and removes empty owned
parents. Modified files, conflicts, and redirected parents fail before
restoration; later user files remain. Instruction symlinks within home and
config roots retain their entries. Existing shared skill aliases stay intact.

`mcp` serves five read-only tools over stdio: status, history,
reap_candidates, gc_candidates, and explain_lane. It validates lifecycle,
IDs, message size, and argument types; no execution, deletion, or signalling
option is accepted. Candidate queries leave matching files untouched.

Fake-HOME tests cover preview, install, idempotency, byte-identical uninstall,
permission restoration, conflict preservation, interrupted install/uninstall,
symlink handling, Aider forms, private manifests, and both hook modes. A
compiled-binary test executes the installed hook and its returned Bash
command through an ffmpeg shim in a single-slot lane, preserves quoted argv,
and uninstalls. A real MCP subprocess exercises all five tools and rejects
unsafe calls. Every HOME and XDG path is temporary.

Red evidence: the shim flow waited on its parent's slot and exited 143 on
cancellation. Passing the existing admission marker to the child shell fixed
it. Linux also rejected Bash conditionals under sh (127, [[: not found);
the wrapper now uses Bash. Tests also caught null request IDs/arguments,
malformed manifests, and indentless Aider lists. Both full gates pass without
weakening assertions. The no-ai-slop pass was applied to the new prose.

Pending: complete only the Docs item next. Read SPEC.md, TASKS.md,
docs/architecture.md, and existing docs before writing README, configuration
reference, recipes, demo.tape, comparison, and community files. Preserve the
implemented behavior and its documented limits. Distribution, stress results,
and the final gate remain separate iterations.

Limits: live agent loading and a real external MCP client's registration were
not exercised. Hook classification is advisory; scripts and shell expansions
can hide work. Keep its installed binary path stable; uninstall/reinstall
before changing hook mode or binary location. Complex Aider YAML requires
conversion to a supported filename-list form. Filesystem validation is not a
sandbox against hostile same-user races. Prior browser, cache, gc, reap,
scheduling, and platform limits remain in docs/architecture.md.
