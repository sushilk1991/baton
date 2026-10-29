# Agent integration

```sh
baton init --dry-run
baton init --yes
baton init --yes --claude-hook
baton init --uninstall --dry-run
baton init --uninstall --yes
```

Without `--yes`, init previews its changes. `--dry-run` always prevents
writes, even with `--yes`. The optional hook requires an existing
`~/.claude` directory. Restart agent sessions after installing instructions.

The canonical skill is `~/.agents/skills/baton/SKILL.md`. Init detects agent
configuration directories, links their skill directories to this skill,
and appends instructions between `<!-- baton:start -->` and
`<!-- baton:end -->`. Existing files retain their bytes before the appended
block; uninstall restores their exact original bytes and permission bits.

| Agent | Detection | Instructions or skill |
| --- | --- | --- |
| Claude Code | `~/.claude` | `CLAUDE.md`, `skills/baton` |
| Codex | `~/.codex` | `AGENTS.md`, shared skill, `skills/baton` compatibility link |
| Cursor | `~/.cursor` | `skills/baton` |
| Gemini CLI | `~/.gemini` | `GEMINI.md`, `skills/baton` |
| OpenCode | `$XDG_CONFIG_HOME/opencode` | `AGENTS.md`, `skills/baton` |
| Goose | `$XDG_CONFIG_HOME/goose` | `.goosehints`, `skills/baton` |
| Aider | `~/.aider` or `~/.aider.conf.yml` | Adds shared instructions to the config's `read` field |
| Other Agent Skills users | `~/.agents/skills` | Canonical skill and `~/.agents/AGENTS.md` |

`XDG_CONFIG_HOME` defaults to `~/.config`. Instruction symlinks resolving
inside the home or config roots are edited at their targets; the links
remain. Shared skill-directory aliases do not acquire recursive links.
Targets outside these roots fail before publication.

Aider has no native Agent Skills loader. Its config loads
`~/.agents/AGENTS.md` as read-only context. Init supports ordinary filename
scalars, one-line flow lists, block lists, and JSON configurations. YAML
merge keys, document boundaries, aliases, anchors on `read`, and multiline
flow/scalar values require manual conversion to a filename list. Unsupported
forms fail without changing agent files.

The private manifest is
`$XDG_DATA_HOME/baton/integration-manifest.json`. It contains originals and
installed bytes so uninstall can restore exact formatting, including JSON
settings. Keep it private; originals can contain sensitive settings. Init
records the complete plan before writing agent files and serializes its own
management operations with a kernel lock. Reinstall resumes interrupted
publication; uninstall resumes interrupted restoration.

Text outside baton's instruction block and unrelated settings or hook entries
survive reinstall and uninstall. Reinstall leaves these files byte-identical.
Uninstall removes only the unchanged recorded block or entry; edited JSON is
reformatted. Current file modes survive. Changed or duplicated owned blocks,
modified skills, replaced parents, and unowned markers fail with the offending
path before restoration. Resolve that conflict before retrying. Aider YAML
requires an unchanged, unambiguous recorded read-field edit. Uninstall removes
only owned files, links, and empty created directories. New unrelated files
remain. The management lock inode remains under baton data.

## Claude Code hook

The opt-in Bash `PreToolUse` hook invokes the same absolute baton executable
as `baton init _hook`. It merges into `settings.json` in the file's own
indentation and preserves other hook entries. Uninstall restores original bytes when settings are unchanged, or
removes only baton's entry when unrelated settings have changed.

For a matching heavy command it returns `updatedInput.command` containing
an explicit-lane prefix, followed by the original text:

```sh
baton hold --lane encode --pid $$ && ffmpeg -i in.mp4 out.mp4
```

It preserves other tool-input fields. It never returns an `allow` decision,
so normal Claude permissions still apply. Segments already executing
`baton run` are skipped; `BATON=off` bypasses the hook. A temporary keeper
holds capacity until the calling shell exits, including after SIGKILL.
Same-lane descendant shims reuse that reservation and record nested jobs.
The original stays in Claude's Bash or zsh, preserving snapshot functions,
`cd`, and `&>` without consulting `$SHELL`. Its shell syntax remains visible
to permission checks. The keeper records its reservation lifetime, not the
original command's exit status or RSS. Keep the executable at the installed
path. Uninstall before changing its location or switching hook modes, then
install again.

The classifier checks executable names in each shell segment, after variable
assignments and `sudo`, `time`, `nice`, or `env` wrappers. It recognizes
install/build operations, npm-style tool launchers, and Python `-m` modules.
Arguments such as `grep ffmpeg` never select a lane. The first matching
segment selects the lane for the whole command. Quoted separators,
comments, and redirection targets are ignored. Shell expansions, functions, scripts, here-docs, and `env -S`
are opaque. Use explicit `baton run` or shims for those forms.
This hook coordinates work; it does not enforce a security boundary.
Both hook modes need `baton hold`, so they run on macOS and Linux only.
On Windows, `init` refuses the hook flags, and an installed hook leaves
every command unchanged.
For clients without input-rewrite support, install with
`baton init --yes --claude-hook-block` instead. That hook exits 2 and prints
the exact queued command on stderr. Retrying that command passes through.
The hook's JSON and shell execution are tested; loading it inside a real
Claude session remains unverified.

## Read-only MCP server

Launch `baton mcp` as a stdio MCP server. It accepts newline-delimited
JSON-RPC, negotiates protocol versions, and writes only protocol messages to
stdout. A client must initialize, then send `notifications/initialized`.

```json
{"command":"baton","args":["mcp"]}
```

Use this command/args pair in your client's MCP server configuration. Init
does not modify client-specific MCP registrations.

| Tool | Arguments | Result |
| --- | --- | --- |
| `status` | `{}` | JSON scheduler and memory snapshot |
| `history` | Optional `lane`, `since` (e.g. `24h`) | JSONL completed jobs; `[]` when empty |
| `reap_candidates` | `{}` | Candidate process listing; no signals |
| `gc_candidates` | `{}` | Candidate file listing; no deletion |
| `explain_lane` | `{"argv":["ffmpeg","-i","a","b"]}` | Selected lane and admission policy |

Tool results use MCP text content. Unknown tools, extra arguments, and
wrong argument types return protocol errors. Domain failures return
`isError: true`. Requests are handled serially, with a 1 MiB message limit;
the server advertises no mutation, task, or subscription capabilities.

Formats checked against the primary references:
[Agent Skills](https://agentskills.io/specification),
[Claude skills](https://code.claude.com/docs/en/skills),
[Claude hooks](https://code.claude.com/docs/en/hooks),
[Codex skills](https://learn.chatgpt.com/docs/build-skills),
[Cursor skills](https://cursor.com/docs/skills),
[Gemini skills](https://geminicli.com/docs/cli/skills/),
[OpenCode skills](https://opencode.ai/docs/skills/),
[Aider conventions](https://aider.chat/docs/usage/conventions.html),
[Goose skills](https://goose-docs.ai/docs/guides/context-engineering/using-skills/),
and the [MCP specification](https://modelcontextprotocol.io/specification/2025-11-25).
