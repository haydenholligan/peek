package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestCodexSentMessagesStayWithTheirTurns(t *testing.T) {
	path := filepath.Join(isolate(t), "rollout-messages.jsonl")
	user := func(turn, id string, content ...map[string]any) any {
		return map[string]any{"type": "event_msg", "payload": map[string]any{
			"type": "item_completed", "turn_id": turn,
			"item": map[string]any{"type": "UserMessage", "id": id, "content": content},
		}}
	}
	complete := func(turn, reply string) any {
		return map[string]any{"type": "event_msg", "payload": map[string]any{
			"type": "task_complete", "turn_id": turn, "last_agent_message": reply,
		}}
	}
	first := user("a", "1", text("First question"))
	writeJSONL(t, path,
		map[string]any{"type": "response_item", "payload": map[string]any{"role": "user", "content": "Automatic context"}},
		first, first,
		user("b", "2", text("Different turn")),
		user("a", "3", text("Also this"), map[string]any{"type": "local_image", "path": "/private/example/chart.png"}),
		complete("b", "Answer B"), complete("a", "Answer A"),
		complete("old", "Reply without recoverable prompt"),
		user("c", "4", map[string]any{"type": "image", "image_url": "data:private"}),
		complete("c", ""),
	)
	got, err := conversation(path)
	want := []exchange{
		{sent: []string{"Different turn"}, reply: "Answer B"},
		{sent: []string{"First question", "Also this\n\nAttachment: chart.png"}, reply: "Answer A"},
		{reply: "Reply without recoverable prompt"},
		{sent: []string{"Attachment: Image 1"}},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, %v; want %#v", got, err, want)
	}
}

func TestClaudeSentMessagesFollowParentsAndExcludeAutomaticContent(t *testing.T) {
	path := filepath.Join(isolate(t), "messages.jsonl")
	user := func(id, parent string, content any, extra map[string]any) map[string]any {
		row := map[string]any{"type": "user", "uuid": id, "parentUuid": parent,
			"turnOrigin": "human", "promptSource": "typed",
			"message": map[string]any{"role": "user", "content": content}}
		for k, v := range extra {
			row[k] = v
		}
		return row
	}
	answer := func(id, parent, message, body string) any {
		return assistant(message, "end_turn", map[string]any{"uuid": id, "parentUuid": parent}, text(body))
	}
	first := user("u1", "", "Question", nil)
	writeJSONL(t, path,
		first, first,
		user("meta", "u1", "Automatic instructions", map[string]any{"isMeta": true}),
		user("u2", "meta", []any{text("Follow-up"), map[string]any{"type": "image", "source": map[string]any{"data": "private"}}}, nil),
		user("tool", "u2", []any{map[string]any{"type": "tool_result", "content": "Tool output"}}, nil),
		answer("a1", "tool", "reply1", "Answer"),
		user("notice", "a1", "Interruption notice", map[string]any{"interruptedMessageId": "reply1"}),
		user("other", "a1", "Abandoned branch", nil),
		user("u3", "a1", "Actual branch", map[string]any{"turnOrigin": ""}),
		answer("a2", "u3", "reply2", "Branch answer"),
		user("system", "a2", "Task notification", map[string]any{"turnOrigin": "task_notification", "promptSource": "system"}),
		user("sdk", "system", "SDK input", map[string]any{"turnOrigin": "sdk"}),
		user("side", "a2", "Subagent input", map[string]any{"isSidechain": true}),
		user("pending", "a2", "Still waiting", nil),
	)
	got, err := conversation(path)
	want := []exchange{
		{sent: []string{"Question", "Follow-up\n\nAttachment: Image 1"}, reply: "Answer"},
		{sent: []string{"Actual branch"}, reply: "Branch answer"},
		{sent: []string{"Still waiting"}},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, %v; want %#v", got, err, want)
	}
}

func TestReaderTogglesSentMessagesAndShowsPending(t *testing.T) {
	entries := []exchange{
		{sent: []string{"Earlier question"}, reply: "Earlier answer"},
		{sent: []string{"Current question", "Extra request"}, reply: strings.Repeat("Long answer.\n\n", 60)},
		{sent: []string{"Unanswered question"}},
	}
	r := newReader(entries, false)
	r.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	key := func(s string) { r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}) }
	view := func() string { return ansi.Strip(r.View()) }
	if r.index != 1 || strings.Contains(view(), "Current question") || !strings.Contains(view(), "Reply 2 of 2") {
		t.Fatalf("should start on latest reply with sent hidden:\n%s", view())
	}
	key("G")
	key("s")
	if r.view.YOffset != 0 || !strings.Contains(view(), "Current question") || !strings.Contains(view(), "Extra request") {
		t.Fatalf("toggle should reveal both messages at the top:\n%s", view())
	}
	key("[")
	if !strings.Contains(view(), "Earlier question") {
		t.Fatalf("toggle should carry across navigation:\n%s", view())
	}
	key("s")
	if strings.Contains(view(), "Earlier question") {
		t.Fatal("toggle should hide sent messages")
	}
	key("]")
	key("]")
	if !strings.Contains(view(), "Unanswered question") || !strings.Contains(view(), "Waiting for") {
		t.Fatalf("pending messages should always be visible:\n%s", view())
	}
	key("s")
	key("s")
	if !strings.Contains(view(), "Unanswered question") {
		t.Fatal("toggle must not hide pending messages")
	}
	reopened := newReader(entries, false)
	if reopened.showSent || reopened.index != 1 {
		t.Fatal("reopening should reset visibility and select latest finished reply")
	}
}

func TestReaderWithOnlyUnansweredMessages(t *testing.T) {
	r := newReader([]exchange{{sent: []string{"First question"}}}, false)
	r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if view := ansi.Strip(r.View()); !strings.Contains(view, "First question") || !strings.Contains(view, "Waiting for reply") {
		t.Fatalf("first prompt should be readable before any reply:\n%s", view)
	}
}
