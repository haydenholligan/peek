# peek

Read your coding agent's replies as clean Markdown, right where you are.
Press a key and the reply fills the pane: headings, lists, tables and
highlighted code. Press **Esc** and you're back exactly where you were. Your
draft is intact, and the agent never stopped working.

Works with **Claude Code** and **Codex**, in any terminal app, as a single
binary with no runtime.

## Install

```sh
go install github.com/haydenholligan/peek@latest
```

Then run `peek setup` for the exact lines to add, with your binary's path
filled in.

## Three ways to use it

| You run agents… | Shortcut | Setup |
|---|---|---|
| in a plain terminal | **Ctrl+\\** | none: start them with `peek claude` or `peek codex` |
| inside your own tmux | **Ctrl+\\** | one `bind` line in `~/.tmux.conf` |
| inside your own zellij | **Alt+R** | one `bind` line in `config.kdl`, plus a SessionStart hook |

`peek claude` and `peek codex` pass any further arguments to the agent, for
example `peek codex resume` or `peek claude --continue`.

## In the reader

| Key | Action |
|---|---|
| Esc, q, Ctrl+\\ | back to the agent |
| ↑ ↓ j k, Space, PgUp/PgDn | scroll |
| g / G | top / bottom |
| [ and ] | earlier / later reply in this conversation |

It opens on the latest finished reply. On macOS it follows your Light/Dark
appearance each time it opens and paints its own background to match, so it stays
readable even when your terminal's theme differs from the Mac's.

## How it works

- **Showing it:** the reader runs in a full-size tmux popup or zellij floating
  pane on top of the agent. The agent's pane is never touched, so nothing is
  lost. `peek claude` and `peek codex` start the agent inside a private,
  invisible tmux server that owns just the shortcut. That server doesn't read
  or change your tmux config.
- **Finding the conversation:** reply text comes from the agent's own transcript,
  so the Markdown is exact.
  - Claude Code keeps `~/.claude/sessions/<pid>.json` for each running session.
  - Codex normally holds its `rollout-*.jsonl` transcript open, which `lsof` can see.
    When a shared background server owns the transcript, tmux users can also be
    matched by the exact session ID in Codex's bottom status line. This fallback
    requires both the session ID and `Context …% left` to be visible on that line;
    reply content still comes from the transcript.
  - peek walks the processes in the pane and uses the agent nearest the top, so
    subagents and other panes are never mistaken for it. If it can't tell, it
    says so instead of guessing.
- **zellij:** zellij can't say which process runs in a pane. So `peek hook`,
  run by Claude Code or Codex at session start, records
  pane → transcript in `~/.local/state/peek/`.

## Limits

- It reads each agent's internal transcript format, which could change.
  Verified with Claude Code 2.1 and Codex CLI 0.157.
- Replies still being written aren't shown until the turn finishes.
- Terminal Markdown: no images or Mermaid diagrams.
- zellij can't bind Ctrl+\\, so it uses Alt+R. On macOS that needs "Option as
  Alt" in your terminal, which zellij's own shortcuts already assume.

## Development

```sh
go test ./...        # includes a real tmux test: open, Esc, draft preserved
go test -short ./... # skip it
```
