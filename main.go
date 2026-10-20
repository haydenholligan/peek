// peek shows a coding agent's latest reply as formatted Markdown, over the
// agent's own terminal pane. Escape returns to the agent untouched.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Ctrl+G is taken in both Codex and Claude Code (edit prompt in $EDITOR).
// zellij can't bind Ctrl+\, so it gets Alt+R, in line with its own Alt shortcuts.
const (
	keyLabel  = `Ctrl+\`
	tmuxKey   = `C-\`
	zellijKey = "Alt r"
)

var agents = map[string]bool{"claude": true, "codex": true}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	var err error
	switch cmd := args[0]; {
	case agents[cmd]:
		var code int
		code, err = launch(cmd, args[1:])
		if err == nil {
			os.Exit(code)
		}
	case cmd == "show":
		err = show(args[1:])
	case cmd == "hook":
		// Never disturb the agent's startup.
		_ = runHook(os.Stdin, os.Getenv)
	case cmd == "setup":
		fmt.Print(setupText())
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "peek:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `peek: read your agent's replies as Markdown, in place. %s opens, Esc returns.

  peek claude [args]       launch Claude Code with the shortcut (no tmux/zellij needed)
  peek codex [args]        launch Codex with the shortcut
  peek setup               keybindings for your own tmux or zellij
  peek show --tmux | --zellij | --pid PID
  peek hook                SessionStart hook for Claude Code and Codex (zellij only)
`, keyLabel)
}

func show(args []string) error {
	flags := flag.NewFlagSet("show", flag.ContinueOnError)
	inZellij := flags.Bool("zellij", false, "read the pane under this floating pane")
	inTmux := flags.Bool("tmux", false, "read the pane under this tmux popup")
	pid := flags.Int("pid", 0, "read the agent under this process")
	if err := flags.Parse(args); err != nil {
		return err
	}
	path, err := func() (string, error) {
		switch {
		case *inZellij:
			return zellijTranscript(os.Getenv)
		case *inTmux:
			// Formats aren't expanded in display-popup's command, so ask from inside the popup:
			// the client's current pane is the one beneath it.
			out, err := exec.Command("tmux", "display-message", "-p", "#{pane_pid}").Output()
			if err != nil {
				return "", fmt.Errorf("asking tmux for the pane: %w", err)
			}
			root, err := strconv.Atoi(strings.TrimSpace(string(out)))
			if err != nil {
				return "", fmt.Errorf("asking tmux for the pane: %w", err)
			}
			return findPaneTranscript(root)
		case *pid != 0:
			return findPaneTranscript(*pid)
		}
		return "", errors.New("show needs --tmux, --zellij or --pid")
	}()
	var all []exchange
	if err == nil {
		all, err = conversation(path)
	}
	if err != nil {
		// Explain in the reader itself: stderr vanishes when the popup closes.
		all = []exchange{{reply: "# Nothing to read yet\n\n" + err.Error() + "."}}
	}
	return runReader(all)
}

func self() string {
	exe, err := os.Executable()
	if err != nil {
		return "peek"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// launch runs the agent in a private, invisible tmux server that owns only the shortcut.
// It leaves the user's own tmux configuration and sessions alone.
func launch(agent string, args []string) (int, error) {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	for _, need := range []string{"tmux", "lsof", agent} {
		if _, err := exec.LookPath(need); err != nil {
			return 0, fmt.Errorf("%s not found on PATH", need)
		}
	}
	if os.Getenv("TMUX") != "" || os.Getenv("ZELLIJ") != "" {
		return 0, errors.New("already inside tmux or zellij; add the keybinding from `peek setup` instead")
	}
	dir, err := os.MkdirTemp("", "peek-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	// Loaded before the agent starts, so even an agent that fails instantly leaves its
	// pane behind: we detach, then show what it printed and pass on its exit status.
	conf := filepath.Join(dir, "tmux.conf")
	if err := os.WriteFile(conf, []byte(`set -g status off
set -g prefix None
set -g escape-time 10
set -g set-clipboard on
set -g focus-events on
set -g mouse on
set -g remain-on-exit on
set -g remain-on-exit-format ""
set-hook -g pane-died detach-client
`), 0o644); err != nil {
		return 0, err
	}
	tmux := []string{"-S", filepath.Join(dir, "tmux.sock"), "-f", conf}
	run := func(a ...string) (string, error) {
		out, err := exec.Command("tmux", append(tmux, a...)...).Output()
		return strings.TrimRight(string(out), " \n"), err
	}
	defer run("kill-server")

	cwd, _ := os.Getwd()
	width, height := terminalSize()
	// Launch the agent as the pane's own process.
	if _, err := run(append([]string{"new-session", "-d", "-s", "peek", "-x", width, "-y", height, "-c", cwd, "--", agent}, args...)...); err != nil {
		return 0, fmt.Errorf("starting tmux: %w", err)
	}
	pid, err := run("display-message", "-p", "-t", "peek", "#{pane_pid}")
	if err != nil {
		return 0, err
	}
	if _, err := run("bind-key", "-n", tmuxKey, "display-popup", "-E", "-B", "-w", "100%", "-h", "100%",
		self(), "show", "--pid", pid); err != nil {
		return 0, err
	}
	if dead, _ := run("display-message", "-p", "-t", "peek", "#{pane_dead}"); dead != "1" {
		attach := exec.Command("tmux", append(tmux, "attach-session", "-t", "peek")...)
		attach.Stdin, attach.Stdout, attach.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := attach.Run(); err != nil {
			return 0, fmt.Errorf("attaching to tmux: %w", err)
		}
		fmt.Print("\x1b[1A\x1b[2K\r") // erase tmux's "[detached (from session peek)]"
	}
	// The agent's last screen, as it would remain after running it directly. tmux scrolls
	// a dead pane's output up into history, so take a screenful of that too.
	if screen, err := run("capture-pane", "-p", "-e", "-S", "-"+height, "-t", "peek"); err == nil {
		if screen = strings.Trim(screen, "\n"); screen != "" {
			fmt.Println(screen)
		}
	}
	status, _ := run("display-message", "-p", "-t", "peek", "#{pane_dead_status}")
	code, _ := strconv.Atoi(status)
	return code, nil
}

func terminalSize() (string, string) {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if fields := strings.Fields(string(out)); err == nil && len(fields) == 2 {
		return fields[1], fields[0]
	}
	return "80", "24"
}

func setupText() string {
	exe := self()
	return fmt.Sprintf(`peek setup. Nothing here is applied automatically.
%[1]s opens the reader over the agent; Esc returns.

## No tmux or zellij
Launch agents through peek. Nothing else to set up:

    peek claude [claude args]
    peek codex [codex args]

## Your own tmux
Add to ~/.tmux.conf, then run: tmux source ~/.tmux.conf

    bind -n '%[2]s' display-popup -E -B -w 100%% -h 100%% "%[4]s show --tmux"

## Your own zellij (Alt+R instead: zellij can't bind %[1]s)
1. Add inside keybinds { shared { ... } } in ~/.config/zellij/config.kdl:

    bind "%[3]s" { Run "%[4]s" "show" "--zellij" { floating true; close_on_exit true; x "0"; y "0"; width "100%%"; height "100%%"; borderless true; }; }

2. zellij can't tell which program runs in a pane, so agents record it when a
   session starts. Merge this into ~/.claude/settings.json and ~/.codex/hooks.json
   (Codex asks you to trust it once), then restart your agents:

    {"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "%[4]s hook"}]}]}}
`, keyLabel, tmuxKey, zellijKey, exe)
}
