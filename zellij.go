package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// zellij can't say which process runs in a pane, so agents record it
// through a SessionStart hook, keyed by the pane's zellij ids.

var listPanes = func() ([]byte, error) {
	return exec.Command("zellij", "action", "list-panes", "--json", "--all").Output()
}

func stateDir() string {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "peek")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "state", "peek")
}

func paneRecord(session, pane string) string {
	return filepath.Join(stateDir(), "zellij", strings.ReplaceAll(session, "/", "_"), pane)
}

// runHook handles a Claude Code or Codex SessionStart event. It must print nothing:
// Claude Code shows hook output to the model.
func runHook(stdin io.Reader, getenv func(string) string) error {
	session, pane := getenv("ZELLIJ_SESSION_NAME"), getenv("ZELLIJ_PANE_ID")
	if session == "" || pane == "" {
		return nil
	}
	var event struct {
		TranscriptPath string `json:"transcript_path"`
		SessionID      string `json:"session_id"`
		AgentID        string `json:"agent_id"`
	}
	if err := json.NewDecoder(stdin).Decode(&event); err != nil {
		return err
	}
	if event.AgentID != "" {
		return nil // Subagents share the pane but not the conversation.
	}
	path := event.TranscriptPath
	if path == "" && event.SessionID != "" {
		path = codexByID(event.SessionID)
	}
	if path == "" {
		return nil
	}
	if strings.HasPrefix(filepath.Base(path), "rollout-") && fileExists(path) && !isPrimaryCodex(path) {
		return nil
	}
	record := paneRecord(session, pane)
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		return err
	}
	tmp := record + ".tmp"
	if err := os.WriteFile(tmp, []byte(path), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, record)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// zellijTranscript finds the pane under the reader's floating pane: zellij keeps
// the tab's tiled focus while a floating pane is open.
func zellijTranscript(getenv func(string) string) (string, error) {
	session, own := getenv("ZELLIJ_SESSION_NAME"), getenv("ZELLIJ_PANE_ID")
	if session == "" || own == "" {
		return "", errors.New("not running inside zellij")
	}
	out, err := listPanes()
	if err != nil {
		return "", err
	}
	var panes []struct {
		ID       int  `json:"id"`
		Plugin   bool `json:"is_plugin"`
		Tab      int  `json:"tab_id"`
		Focused  bool `json:"is_focused"`
		Floating bool `json:"is_floating"`
	}
	if err := json.Unmarshal(out, &panes); err != nil {
		return "", err
	}
	tab := -1
	for _, p := range panes {
		if !p.Plugin && strconv.Itoa(p.ID) == own {
			tab = p.Tab
		}
	}
	var targets []string
	for _, p := range panes {
		if !p.Plugin && p.Tab == tab && p.Focused && !p.Floating && strconv.Itoa(p.ID) != own {
			targets = append(targets, strconv.Itoa(p.ID))
		}
	}
	if len(targets) != 1 {
		return "", errors.New("couldn't tell which zellij pane to read; focus the agent's pane and try again")
	}
	path, err := os.ReadFile(paneRecord(session, targets[0]))
	if err != nil {
		return "", errors.New("this pane's conversation is unknown. Add the SessionStart hook from `peek setup`, then restart the agent in this pane")
	}
	return string(path), nil
}
