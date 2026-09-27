package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type process struct {
	pid, ppid int
	args      string
}

// Swapped out in tests.
var (
	listProcesses = psProcesses
	openFiles     = lsofFiles
)

func home(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, fallback)
}

func claudeHome() string { return home("CLAUDE_CONFIG_DIR", ".claude") }
func codexHome() string  { return home("CODEX_HOME", ".codex") }

func psProcesses() ([]process, error) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil, err
	}
	var procs []process
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		procs = append(procs, process{pid, ppid, strings.Join(fields[2:], " ")})
	}
	return procs, nil
}

// lsofFiles returns the Codex transcripts each process holds open.
func lsofFiles(pids []int) map[int][]string {
	list := make([]string, len(pids))
	for i, pid := range pids {
		list[i] = strconv.Itoa(pid)
	}
	out, _ := exec.Command("lsof", "-nP", "-p", strings.Join(list, ","), "-Fn").Output()
	files := map[int][]string{}
	pid := 0
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ = strconv.Atoi(line[1:])
		case strings.HasPrefix(line, "n"):
			name := filepath.Base(line[1:])
			if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl") {
				files[pid] = append(files[pid], line[1:])
			}
		}
	}
	return files
}

// descendants lists root and everything below it, nearest first.
func descendants(root int, procs []process) []process {
	children := map[int][]process{}
	var tree []process
	for _, p := range procs {
		children[p.ppid] = append(children[p.ppid], p)
		if p.pid == root {
			tree = append(tree, p)
		}
	}
	if tree == nil {
		tree = []process{{pid: root}}
	}
	for i := 0; i < len(tree); i++ {
		tree = append(tree, children[tree[i].pid]...)
	}
	return tree
}

// claudeTranscript uses the per-process session file Claude Code keeps while it runs.
func claudeTranscript(pid int) string {
	data, err := os.ReadFile(filepath.Join(claudeHome(), "sessions", strconv.Itoa(pid)+".json"))
	if err != nil {
		return ""
	}
	var session struct {
		SessionID string `json:"sessionId"`
		Kind      string `json:"kind"`
	}
	if json.Unmarshal(data, &session) != nil || session.SessionID == "" ||
		(session.Kind != "" && session.Kind != "interactive") {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(claudeHome(), "projects", "*", session.SessionID+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

func codexByID(id string) string {
	matches, _ := filepath.Glob(filepath.Join(codexHome(), "sessions", "*", "*", "*", "rollout-*"+id+".jsonl"))
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

// codexResumed covers `codex resume <id>` before Codex has reopened its transcript.
func codexResumed(args string) string {
	fields := strings.Fields(args)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "resume" && !strings.HasPrefix(fields[i+1], "-") &&
			strings.Contains(strings.Join(fields[:i], " "), "codex") {
			return codexByID(fields[i+1])
		}
	}
	return ""
}

func firstLine(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadBytes('\n')
	return line
}

// isPrimaryCodex rejects subagent transcripts, whose source is an object rather than a name.
func isPrimaryCodex(path string) bool {
	var meta struct {
		Payload struct {
			Source json.RawMessage `json:"source"`
		} `json:"payload"`
	}
	if json.Unmarshal(firstLine(path), &meta) != nil {
		return false
	}
	return bytes.HasPrefix(bytes.TrimSpace(meta.Payload.Source), []byte(`"`))
}

// findTranscript returns the conversation owned by the agent nearest to root.
func findTranscript(root int) (string, error) {
	procs, err := listProcesses()
	if err != nil {
		return "", err
	}
	tree := descendants(root, procs)
	var open map[int][]string
	for _, p := range tree {
		if path := claudeTranscript(p.pid); path != "" {
			return path, nil
		}
		if open == nil {
			pids := make([]int, len(tree))
			for i, q := range tree {
				pids[i] = q.pid
			}
			open = openFiles(pids)
		}
		var primary []string
		for _, path := range open[p.pid] {
			if isPrimaryCodex(path) {
				primary = append(primary, path)
			}
		}
		if len(primary) > 1 {
			return "", errors.New("several Codex conversations are open in this pane, so none was picked")
		}
		if len(primary) == 1 {
			return primary[0], nil
		}
		if path := codexResumed(p.args); path != "" {
			return path, nil
		}
	}
	return "", errors.New("no Codex or Claude Code conversation found in this pane yet")
}

func eachLine(path string, fn func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<16)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			fn(line) // A running session may end with a partial line; it fails to parse and is skipped.
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func codexReplies(path string) ([]string, error) {
	var replies []string
	err := eachLine(path, func(line []byte) {
		if !bytes.Contains(line, []byte(`"task_complete"`)) {
			return // Skip decoding the bulk of a long session.
		}
		var e struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string  `json:"type"`
				Message *string `json:"last_agent_message"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &e) == nil && e.Type == "event_msg" && e.Payload.Type == "task_complete" &&
			e.Payload.Message != nil && strings.TrimSpace(*e.Payload.Message) != "" {
			replies = append(replies, *e.Payload.Message)
		}
	})
	return replies, err
}

// claudeReplies joins the text of each finished assistant message.
// Claude Code writes one message across several lines that share an id.
func claudeReplies(path string) ([]string, error) {
	var order []string
	texts := map[string][]string{}
	finished := map[string]bool{}
	err := eachLine(path, func(line []byte) {
		if !bytes.Contains(line, []byte(`"assistant"`)) {
			return
		}
		var e struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Message     struct {
				ID         string          `json:"id"`
				StopReason string          `json:"stop_reason"`
				Content    json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &e) != nil || e.Type != "assistant" || e.IsSidechain || e.Message.ID == "" {
			return
		}
		id := e.Message.ID
		if _, seen := texts[id]; !seen {
			order = append(order, id)
			texts[id] = nil
		}
		finished[id] = finished[id] || e.Message.StopReason == "end_turn"
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(e.Message.Content, &blocks) == nil {
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					texts[id] = append(texts[id], b.Text)
				}
			}
		}
	})
	var replies []string
	for _, id := range order {
		if text := strings.Join(texts[id], "\n\n"); finished[id] && strings.TrimSpace(text) != "" {
			replies = append(replies, text)
		}
	}
	return replies, err
}

func replies(path string) ([]string, error) {
	read := claudeReplies
	if strings.HasPrefix(filepath.Base(path), "rollout-") {
		read = codexReplies
	}
	all, err := read(path)
	if err == nil && len(all) == 0 {
		err = fmt.Errorf("no finished reply yet in %s; let the agent finish, then try again", filepath.Base(path))
	}
	return all, err
}
