package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func writeJSONL(t *testing.T, path string, rows ...any) {
	t.Helper()
	var b strings.Builder
	for _, r := range rows {
		line, _ := json.Marshal(r)
		b.Write(line)
		b.WriteByte('\n')
	}
	b.WriteString(`{"type":`) // a running session may end mid-write
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assistant(id, stop string, extra map[string]any, blocks ...map[string]any) map[string]any {
	row := map[string]any{"type": "assistant", "message": map[string]any{"id": id, "stop_reason": stop, "content": blocks}}
	for k, v := range extra {
		row[k] = v
	}
	return row
}

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func isolate(t *testing.T) string {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(dir, "codex"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	return dir
}

func TestClaudeRepliesJoinFinishedMessagesOnly(t *testing.T) {
	path := filepath.Join(isolate(t), "s.jsonl")
	writeJSONL(t, path,
		assistant("a", "end_turn", nil, text("Old answer")),
		map[string]any{"type": "user", "message": map[string]any{"content": "next"}},
		assistant("b", "end_turn", nil, map[string]any{"type": "thinking", "thinking": "hmm"}),
		assistant("b", "end_turn", nil, text("# Latest")),
		map[string]any{"type": "attachment"},
		assistant("b", "end_turn", nil, text("Bonjour é 🌞")),
		assistant("c", "end_turn", map[string]any{"isSidechain": true}, text("Subagent")),
		assistant("d", "tool_use", nil, text("Still working"), map[string]any{"type": "tool_use"}),
	)
	got, err := replies(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Old answer", "# Latest\n\nBonjour é 🌞"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}

func TestCodexRepliesUseCompletedTurns(t *testing.T) {
	path := filepath.Join(isolate(t), "rollout-x.jsonl")
	complete := func(s string) map[string]any {
		return map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_complete", "last_agent_message": s}}
	}
	writeJSONL(t, path, complete("One"), complete("Two é"),
		map[string]any{"type": "response_item", "payload": map[string]any{"role": "assistant"}})
	got, err := replies(path)
	if err != nil || strings.Join(got, "|") != "One|Two é" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestNoFinishedReplyIsExplained(t *testing.T) {
	path := filepath.Join(isolate(t), "rollout-x.jsonl")
	writeJSONL(t, path, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_started"}})
	if _, err := replies(path); err == nil || !strings.Contains(err.Error(), "no finished reply") {
		t.Fatalf("got %v", err)
	}
}

func stub(t *testing.T, procs []process, files map[int][]string) {
	oldP, oldF := listProcesses, openFiles
	listProcesses = func() ([]process, error) { return procs, nil }
	openFiles = func([]int) map[int][]string { return files }
	t.Cleanup(func() { listProcesses, openFiles = oldP, oldF })
}

func codexMeta(t *testing.T, path, source string) {
	writeJSONL(t, path, map[string]any{"type": "session_meta", "payload": map[string]any{"source": json.RawMessage(source)}})
}

func TestNearestAgentInThePaneWins(t *testing.T) {
	dir := isolate(t)
	session := filepath.Join(dir, "claude", "sessions", "11.json")
	os.MkdirAll(filepath.Dir(session), 0o755)
	os.WriteFile(session, []byte(`{"pid":11,"sessionId":"abc","kind":"interactive"}`), 0o644)
	claude := filepath.Join(dir, "claude", "projects", "-work", "abc.jsonl")
	writeJSONL(t, claude)
	primary, child := filepath.Join(dir, "rollout-main.jsonl"), filepath.Join(dir, "rollout-child.jsonl")
	codexMeta(t, primary, `"cli"`)
	codexMeta(t, child, `{"subagent":{}}`)
	procs := []process{{10, 1, "zsh"}, {11, 10, "claude"}, {20, 1, "zsh"}, {21, 20, "codex"}, {22, 21, "codex"}, {30, 1, "other"}}
	stub(t, procs, map[int][]string{21: {child, primary}, 22: {child}})

	if got, _ := findTranscript(10); got != claude {
		t.Fatalf("claude pane: got %q", got)
	}
	if got, _ := findTranscript(20); got != primary {
		t.Fatalf("codex pane: got %q", got)
	}
	if _, err := findTranscript(30); err == nil {
		t.Fatal("unrelated pane should not guess")
	}
}

func TestAmbiguousCodexIsNotGuessed(t *testing.T) {
	dir := isolate(t)
	a, b := filepath.Join(dir, "rollout-a.jsonl"), filepath.Join(dir, "rollout-b.jsonl")
	codexMeta(t, a, `"cli"`)
	codexMeta(t, b, `"cli"`)
	stub(t, []process{{10, 1, "codex"}}, map[int][]string{10: {a, b}})
	if _, err := findTranscript(10); err == nil || !strings.Contains(err.Error(), "several") {
		t.Fatalf("got %v", err)
	}
}

func TestResumedCodexFoundByID(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "codex", "sessions", "2026", "09", "27", "rollout-2026-09-27T01-00-00-0199aa.jsonl")
	codexMeta(t, path, `"cli"`)
	stub(t, []process{{10, 1, "node /opt/bin/codex resume 0199aa"}}, nil)
	if got, err := findTranscript(10); got != path {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestDaemonCodexFoundFromPaneStatus(t *testing.T) {
	dir := isolate(t)
	id := "01a156de-ba8f-72b0-8a44-d5f4631dedd7"
	path := filepath.Join(codexHome(), "sessions", "2026", "10", "19", "rollout-test-"+id+".jsonl")
	codexMeta(t, path, `"vscode"`)
	stub(t, []process{{10, 1, "node /bin/codex"}, {11, 10, "/bin/codex"}, {20, 1, "codex app-server"}}, map[int][]string{20: {path}})
	t.Setenv("TMUX", "/tmp/test.sock,1,0")
	// A different pane is focused; the lookup must capture the requested root.
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	script := `#!/bin/sh
case "$*" in
  'list-panes -a -F #{pane_pid} #{pane_id}') printf '30 %%0\n10 %%1\n' ;;
  'capture-pane -p -t %1') printf '%s\n' 'Agent reply' 'GPT-6.1-Sol high · ~ · ` + id + ` · Context 83% left · main' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got, err := findPaneTranscript(10); err != nil || got != path {
		t.Fatalf("daemon-backed pane: got %q %v", got, err)
	}
	if _, err := findPaneTranscript(30); err == nil {
		t.Fatal("unrelated pane should not guess")
	}
	// A stale Codex footer after the agent exits must not select its session.
	stub(t, []process{{10, 1, "zsh"}}, nil)
	if _, err := findPaneTranscript(10); err == nil {
		t.Fatal("pane without a running Codex should not use a stale footer")
	}
}

func TestCodexStatusRequiresUnambiguousFooter(t *testing.T) {
	isolate(t)
	id := "01a156de-ba8f-72b0-8a44-d5f4631dedd7"
	path := filepath.Join(codexHome(), "sessions", "2026", "10", "19", "rollout-test-"+id+".jsonl")
	codexMeta(t, path, `"vscode"`)
	footer := "GPT-6.1-Sol high · " + id + " · Context 83% left · main"
	for _, screen := range []string{
		"", id, "Reply mentioning " + id,
		footer + "\nAsk Codex to do anything",
		"GPT high · " + id,
		footer + " · 01a156f2-9e62-7511-89a8-215da4328349",
		strings.ReplaceAll(footer, id, "01a156f2-9e62-7511-89a8-215da4328349"),
	} {
		if got := codexStatusTranscript(screen); got != "" {
			t.Errorf("should reject %q; got %q", screen, got)
		}
	}
	if got := codexStatusTranscript("Reply text\n" + footer + "\n\n"); got != path {
		t.Fatalf("valid footer: got %q", got)
	}
	codexMeta(t, path, `{"subagent":{}}`)
	if got := codexStatusTranscript(footer); got != "" {
		t.Fatalf("subagent should not be selected: %q", got)
	}
}

func TestZellijHookLinksPaneUnderFloatingReader(t *testing.T) {
	isolate(t)
	env := func(pane string) func(string) string {
		return func(k string) string {
			return map[string]string{"ZELLIJ_SESSION_NAME": "work/main", "ZELLIJ_PANE_ID": pane}[k]
		}
	}
	runHook(strings.NewReader(`{"transcript_path":"/x/other.jsonl"}`), env("1"))
	runHook(strings.NewReader(`{"transcript_path":"/x/mine.jsonl"}`), env("2"))
	runHook(strings.NewReader(`{"transcript_path":"/x/sub.jsonl","agent_id":"a"}`), env("2"))
	old := listPanes
	listPanes = func() ([]byte, error) {
		return []byte(`[
			{"id":0,"is_plugin":true,"tab_id":1,"is_focused":true,"is_floating":false},
			{"id":1,"is_plugin":false,"tab_id":0,"is_focused":true,"is_floating":false},
			{"id":2,"is_plugin":false,"tab_id":1,"is_focused":true,"is_floating":false},
			{"id":3,"is_plugin":false,"tab_id":1,"is_focused":false,"is_floating":false},
			{"id":9,"is_plugin":false,"tab_id":1,"is_focused":true,"is_floating":true}]`), nil
	}
	t.Cleanup(func() { listPanes = old })
	if got, err := zellijTranscript(env("9")); got != "/x/mine.jsonl" {
		t.Fatalf("got %q %v", got, err)
	}
	os.RemoveAll(stateDir())
	if _, err := zellijTranscript(env("9")); err == nil || !strings.Contains(err.Error(), "peek setup") {
		t.Fatalf("missing hook should explain setup, got %v", err)
	}
}

func TestHookOutsideZellijDoesNothing(t *testing.T) {
	isolate(t)
	if err := runHook(strings.NewReader(`not json`), func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if fileExists(stateDir()) {
		t.Fatal("wrote state outside zellij")
	}
}

func TestReaderNavigatesAndEscapes(t *testing.T) {
	long := "# Title\n\n| A | B |\n| - | - |\n| 1 | 2 |\n\n" + strings.Repeat("Paragraph.\n\n", 80)
	r := newReader([]string{"First reply", long}, false)
	r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := func() string { return ansi.Strip(r.View()) }
	if !strings.Contains(view(), "Reply 2 of 2") || !strings.Contains(view(), "Title") {
		t.Fatalf("should open on the latest reply:\n%s", view())
	}
	r.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	if r.view.YOffset == 0 {
		t.Fatal("space should page down")
	}
	r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[")})
	if !strings.Contains(view(), "Reply 1 of 2") || !strings.Contains(view(), "First reply") {
		t.Fatalf("[ should show the earlier reply:\n%s", view())
	}
	if _, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd == nil || cmd() != tea.Quit() {
		t.Fatal("esc should quit")
	}
}
