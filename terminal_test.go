package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeCodex = `#!/bin/sh
f="$CODEX_HOME/sessions/2026/09/27/rollout-test.jsonl"
mkdir -p "$(dirname "$f")"
printf '%s\n' '{"type":"session_meta","payload":{"source":"cli"}}' \
  '{"type":"event_msg","payload":{"type":"task_complete","last_agent_message":"# Reader test\n\n**Bonjour** from the right session.\n\n| Col | Val |\n| --- | --- |\n| a | 1 |"}}' > "$f"
exec 3>>"$f"
( i=0; while :; do i=$((i+1)); echo $i > "$T/heartbeat"; sleep 0.1; done ) &
echo FAKE-AGENT-READY
IFS= read -r draft
printf '%s' "$draft" > "$T/draft"
kill $!
`

// TestShortcutKeepsDraftAndAgentRunning drives the real launcher inside an outer
// tmux that stands in for the user's terminal app. No model calls.
func TestShortcutKeepsDraftAndAgentRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("terminal test")
	}
	for _, need := range []string{"tmux", "lsof"} {
		if _, err := exec.LookPath(need); err != nil {
			t.Skip(need + " not installed")
		}
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	if out, err := exec.Command("go", "build", "-o", filepath.Join(bin, "peek"), ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	os.WriteFile(filepath.Join(bin, "codex"), []byte(fakeCodex), 0o755)

	// Unix socket paths are capped at 104 bytes on macOS; t.TempDir is too deep.
	sockets, err := os.MkdirTemp("", "peek-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockets)
	outer := []string{"-S", filepath.Join(sockets, "outer.sock"), "-f", "/dev/null"}
	tmux := func(args ...string) string {
		out, _ := exec.Command("tmux", append(outer, args...)...).Output()
		return string(out)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	command := "env -u TMUX PATH=" + quote(bin+":"+os.Getenv("PATH")) + " CODEX_HOME=" + quote(filepath.Join(dir, "codex")) +
		" CLAUDE_CONFIG_DIR=" + quote(filepath.Join(dir, "claude")) + " T=" + quote(dir) + " peek codex; sleep 30"
	if _, err := exec.Command("tmux", append(outer, "new-session", "-d", "-x", "100", "-y", "30", command)...).Output(); err != nil {
		t.Fatal(err)
	}
	defer tmux("kill-server")

	screen := func() string { return tmux("capture-pane", "-p") }
	until := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if ok() {
				return
			}
		}
		t.Fatalf("timed out waiting for %s. Screen:\n%s", what, screen())
	}
	read := func(name string) string { b, _ := os.ReadFile(filepath.Join(dir, name)); return string(b) }

	until("agent", func() bool { return strings.Contains(screen(), "FAKE-AGENT-READY") })
	tmux("send-keys", "-l", "KEEP-DRAFT")
	until("draft echo", func() bool { return strings.Contains(screen(), "KEEP-DRAFT") })

	tmux("send-keys", `C-\`)
	until("reader", func() bool {
		s := screen()
		return strings.Contains(s, "Bonjour") && strings.Contains(s, "Reply 1 of 1") && !strings.Contains(s, "KEEP-DRAFT")
	})
	// Colors, not just text: the reader paints its own background so it's legible on any
	// terminal theme. (Its absence is what made the first real run unreadable.)
	painted := func() bool {
		s := tmux("capture-pane", "-e", "-p")
		return strings.Contains(s, "48;2;251;251;250") || strings.Contains(s, "48;2;28;29;34")
	}
	until("painted background", painted)
	beat := read("heartbeat")
	until("agent to keep running under the reader", func() bool { return read("heartbeat") != beat })

	tmux("send-keys", "Escape")
	until("agent screen restored", func() bool {
		s := screen()
		return strings.Contains(s, "KEEP-DRAFT") && !strings.Contains(s, "Reply 1 of 1")
	})
	if painted() {
		t.Fatalf("reader colors leaked onto the agent:\n%s", tmux("capture-pane", "-e", "-p"))
	}
	tmux("send-keys", "-l", "--", "-AFTER")
	tmux("send-keys", "Enter")
	until("draft submitted", func() bool { return read("draft") == "KEEP-DRAFT-AFTER" })
}

// TestFailingAgentShowsItsError: an agent that dies at once must not leave a bare
// "[exited]"; its output and exit status come through.
func TestFailingAgentShowsItsError(t *testing.T) {
	if testing.Short() {
		t.Skip("terminal test")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	if out, err := exec.Command("go", "build", "-o", filepath.Join(bin, "peek"), ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\necho 'Error: claude native binary not installed.'\nexit 3\n"), 0o755)
	sockets, err := os.MkdirTemp("", "peek-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockets)
	outer := []string{"-S", filepath.Join(sockets, "outer.sock"), "-f", "/dev/null"}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	command := "env -u TMUX PATH=" + quote(bin+":"+os.Getenv("PATH")) + " peek claude; echo EXIT=$?; sleep 30"
	exec.Command("tmux", append(outer, "new-session", "-d", "-x", "100", "-y", "30", command)...).Run()
	defer exec.Command("tmux", append(outer, "kill-server")...).Run()
	var screen string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		out, _ := exec.Command("tmux", append(outer, "capture-pane", "-p")...).Output()
		if screen = string(out); strings.Contains(screen, "EXIT=") {
			break
		}
	}
	if !strings.Contains(screen, "native binary not installed") || !strings.Contains(screen, "EXIT=3") {
		t.Fatalf("agent's error or status lost. Screen:\n%s", screen)
	}
}
