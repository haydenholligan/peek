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
	"regexp"
	"strconv"
	"strings"
)

type process struct {
	pid, ppid int
	args      string
}

// Swapped out in tests.
var (
	listProcesses   = psProcesses
	openFiles       = lsofFiles
	errNoTranscript = errors.New("no Codex or Claude Code conversation found in this pane yet")
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
	return "", errNoTranscript
}

// A daemon-backed Codex TUI does not own its rollout file. Its status line can
// identify the thread, but only use a complete ID in a recognizable final footer.
var codexStatusID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var codexStatusContext = regexp.MustCompile(`^Context [0-9]+% left$`)

func codexStatusTranscript(screen string) string {
	lines := strings.Split(strings.TrimSpace(screen), "\n")
	fields := strings.Split(lines[len(lines)-1], "·")
	var id string
	context := false
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if codexStatusID.MatchString(field) {
			if id != "" {
				return ""
			}
			id = field
		}
		context = context || codexStatusContext.MatchString(field)
	}
	if id != "" && context {
		if path := codexByID(id); path != "" && isPrimaryCodex(path) {
			return path
		}
	}
	return ""
}

func findPaneTranscript(root int) (string, error) {
	path, err := findTranscript(root)
	if !errors.Is(err, errNoTranscript) || os.Getenv("TMUX") == "" {
		return path, err
	}
	procs, listErr := listProcesses()
	if listErr != nil {
		return "", err
	}
	codex := false
	for _, p := range descendants(root, procs) {
		fields := strings.Fields(p.args)
		if len(fields) > 0 && filepath.Base(fields[0]) == "codex" {
			codex = true
		}
	}
	if !codex {
		return "", err
	}
	// Match the requested process, not the client's focused pane: focus can move
	// while the popup opens, and --pid may refer to another pane entirely.
	out, listErr := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_pid} #{pane_id}").Output()
	if listErr == nil {
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 || fields[0] != strconv.Itoa(root) {
				continue
			}
			screen, captureErr := exec.Command("tmux", "capture-pane", "-p", "-t", fields[1]).Output()
			if captureErr == nil {
				if path := codexStatusTranscript(string(screen)); path != "" {
					return path, nil
				}
			}
		}
	}
	return "", err
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

// An exchange contains a finished reply or sent messages awaiting a reply.
type exchange struct {
	sent  []string
	reply string
}

func codexConversation(path string) ([]exchange, error) {
	var all []exchange
	var completed []int
	turns := map[string]int{}
	seen := map[string]bool{}
	err := eachLine(path, func(line []byte) {
		if !bytes.Contains(line, []byte(`"event_msg"`)) {
			return
		}
		var e struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string `json:"type"`
				TurnID  string `json:"turn_id"`
				Message string `json:"last_agent_message"`
				Item    struct {
					Type    string `json:"type"`
					ID      string `json:"id"`
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
						Path string `json:"path"`
						Name string `json:"name"`
					} `json:"content"`
				} `json:"item"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &e) != nil || e.Type != "event_msg" {
			return
		}
		p := e.Payload
		var sent string
		if p.Type == "item_completed" && p.Item.Type == "UserMessage" {
			if p.Item.ID != "" && seen[p.Item.ID] {
				return
			}
			seen[p.Item.ID] = true
			var parts []string
			images := 0
			for _, b := range p.Item.Content {
				switch b.Type {
				case "text":
					parts = append(parts, b.Text)
				case "local_image", "image":
					images++
					label := fmt.Sprintf("Image %d", images)
					if b.Path != "" {
						label = filepath.Base(b.Path)
					}
					parts = append(parts, "Attachment: "+label)
				case "skill":
					parts = append(parts, "Skill: "+b.Name)
				}
			}
			sent = strings.Join(parts, "\n\n")
		} else if p.Type != "task_complete" || strings.TrimSpace(p.Message) == "" {
			return
		}
		if strings.TrimSpace(sent) == "" && p.Type != "task_complete" {
			return
		}
		i, ok := turns[p.TurnID]
		if !ok || (p.TurnID == "" && all[i].reply != "") {
			i = len(all)
			all = append(all, exchange{})
			turns[p.TurnID] = i
		}
		if sent != "" {
			all[i].sent = append(all[i].sent, sent)
		} else {
			if all[i].reply == "" {
				completed = append(completed, i)
			}
			all[i].reply = p.Message
		}
	})
	// Keep finished replies in completion order, followed by unanswered turns.
	var ordered []exchange
	for _, i := range completed {
		ordered = append(ordered, all[i])
	}
	for _, entry := range all {
		if entry.reply == "" {
			ordered = append(ordered, entry)
		}
	}
	return ordered, err
}

// Claude writes assistant text across several rows sharing a message ID.
func claudeConversation(path string) ([]exchange, error) {
	var all []exchange
	var pending []string
	texts := map[string][]string{}
	finished := map[string]bool{}
	completed := map[string]int{}
	seen := map[string]bool{}
	type node struct {
		parent, messageID, sent string
	}
	nodes := map[string]node{}
	var replyNodes []string
	err := eachLine(path, func(line []byte) {
		var e struct {
			Type                 string `json:"type"`
			UUID                 string `json:"uuid"`
			ParentUUID           string `json:"parentUuid"`
			InterruptedMessageID string `json:"interruptedMessageId"`
			IsSidechain          bool   `json:"isSidechain"`
			IsMeta               bool   `json:"isMeta"`
			IsCompactSummary     bool   `json:"isCompactSummary"`
			TurnOrigin           string `json:"turnOrigin"`
			PromptSource         string `json:"promptSource"`
			Message              struct {
				ID         string          `json:"id"`
				StopReason string          `json:"stop_reason"`
				Content    json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &e) != nil {
			return
		}
		if e.UUID != "" {
			if seen[e.UUID] {
				return
			}
			seen[e.UUID] = true
			nodes[e.UUID] = node{parent: e.ParentUUID, messageID: e.Message.ID}
		}
		if e.IsSidechain || e.IsMeta || e.IsCompactSummary || e.InterruptedMessageID != "" {
			return
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		json.Unmarshal(e.Message.Content, &blocks)
		switch e.Type {
		case "user":
			if (e.TurnOrigin != "" && e.TurnOrigin != "human") ||
				(e.PromptSource != "" && e.PromptSource != "typed" && e.PromptSource != "queued") {
				return
			}
			var parts []string
			var plain string
			if json.Unmarshal(e.Message.Content, &plain) == nil {
				parts = append(parts, plain)
			}
			images := 0
			for _, b := range blocks {
				switch b.Type {
				case "text":
					parts = append(parts, b.Text)
				case "image":
					images++
					parts = append(parts, fmt.Sprintf("Attachment: Image %d", images))
				}
			}
			if sent := strings.Join(parts, "\n\n"); strings.TrimSpace(sent) != "" {
				pending = append(pending, sent)
				if e.UUID != "" {
					n := nodes[e.UUID]
					n.sent = sent
					nodes[e.UUID] = n
				}
			}
		case "assistant":
			id := e.Message.ID
			if id == "" {
				return
			}
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					texts[id] = append(texts[id], b.Text)
				}
			}
			finished[id] = finished[id] || e.Message.StopReason == "end_turn"
			i, recorded := completed[id]
			if !finished[id] {
				return
			}
			reply := strings.Join(texts[id], "\n\n")
			if strings.TrimSpace(reply) == "" {
				return
			}
			if !recorded {
				i = len(all)
				all = append(all, exchange{sent: pending})
				replyNodes = append(replyNodes, e.UUID)
				pending = nil
				completed[id] = i
			}
			all[i].reply = reply
		}
	})
	// Parent links keep prompts from a different branch out of a reply.
	// Older records without UUIDs retain the chronological association above.
	for i, uuid := range replyNodes {
		if uuid == "" {
			continue
		}
		var sent []string
		visited := map[string]bool{}
		for uuid != "" && !visited[uuid] {
			visited[uuid] = true
			n, ok := nodes[uuid]
			if !ok {
				break
			}
			if previous, finished := completed[n.messageID]; finished && previous != i {
				break
			}
			if n.sent != "" {
				sent = append(sent, n.sent)
			}
			uuid = n.parent
		}
		for left, right := 0, len(sent)-1; left < right; left, right = left+1, right-1 {
			sent[left], sent[right] = sent[right], sent[left]
		}
		all[i].sent = sent
	}
	if len(pending) > 0 {
		all = append(all, exchange{sent: pending})
	}
	return all, err
}

func conversation(path string) ([]exchange, error) {
	read := claudeConversation
	if strings.HasPrefix(filepath.Base(path), "rollout-") {
		read = codexConversation
	}
	all, err := read(path)
	if err == nil && len(all) == 0 {
		err = fmt.Errorf("no messages yet in %s; send a message, then try again", filepath.Base(path))
	}
	return all, err
}
