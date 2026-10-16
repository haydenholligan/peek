# Local prototype checkpoint — 2026-10-15

Keep using the existing Go `peek` prototype locally. A rewrite, graphical
reader, and public release remain future decisions.

## Use with Codex

From a normal terminal prompt:

```sh
peek codex
```

To choose an existing conversation:

```sh
peek codex resume
```

Any arguments after `codex` are passed through to Codex.

- **Ctrl+\** opens the latest completed reply as Markdown.
- **Esc** or **q** returns to the agent, preserving your draft.
- **[** and **]** move to earlier and later replies.
- Arrow keys, **j/k**, Space, and Page Up/Down scroll.
- **g/G** jump to the top/bottom.
- On macOS, the reader checks system light/dark appearance when opened.

Start through `peek` before chatting; it cannot add this shortcut to an
already-running plain-terminal agent. If you already use tmux or zellij,
run `peek setup` for integration instructions instead of nesting the launcher.

Local executable: `/Users/hayden/.local/bin/peek`.
Source: `/Users/hayden/Developer/peek`.
The launcher needs `tmux`, `lsof`, and `codex` on PATH.

## Direction saved for later

The goal is a quick, readable view of an agent's response that returns to the
same working context without losing input. Product quality and reliable session
selection matter more than rewriting in a particular language.

Ideas discussed, not implemented or committed as scope:

- Configurable shortcuts with a convenient default.
- Optional proportional typography alongside terminal-style monospace.
- Search and faster transcript discovery, guided by measurements.
- Ways to use the reader without wrapping every agent launch.
- Broader agent, terminal, OS, and remote-session support with explicit limits.

The supplied benchmark figures and rewrite estimates have not been independently
verified. Go versus Rust and terminal versus graphical rendering remain open.

## Local verification

```sh
cd /Users/hayden/Developer/peek
go test -count=1 ./...
```

The existing terminal integration test uses a fake Codex process and real tmux.
It checks opening the reader, rendered text/background, ongoing agent activity,
Escape returning to the agent, and preservation of an unfinished prompt. It
does not make model calls or prove compatibility with every current Codex session.
