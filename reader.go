package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

const maxWidth = 100

// darkMode follows the macOS appearance, read fresh each time the reader opens.
func darkMode() bool {
	if runtime.GOOS != "darwin" {
		return true
	}
	out, _ := exec.Command("/usr/bin/defaults", "read", "-g", "AppleInterfaceStyle").Output()
	return strings.TrimSpace(string(out)) == "Dark"
}

// paint sets the pane's default colors (OSC 10/11) so the theme stays legible even
// when the terminal's own theme doesn't match the Mac, e.g. a dark terminal in Light
// mode. tmux popups and zellij floating panes keep these colors to themselves.
// It returns the sequence that restores the terminal's defaults.
func paint(dark bool) (restore string) {
	fg, bg := "#1f2328", "#fbfbfa"
	if dark {
		fg, bg = "#e4e5e9", "#1c1d22"
	}
	fmt.Printf("\x1b]10;%s\x1b\\\x1b]11;%s\x1b\\", fg, bg)
	return "\x1b]110\x1b\\\x1b]111\x1b\\"
}

type reader struct {
	exchanges []exchange // oldest first
	showSent  bool
	index     int
	dark      bool
	width     int
	view      viewport.Model
	rendered  map[[3]int]string // (exchange, width, sent visibility) -> rendered
}

func newReader(exchanges []exchange, dark bool) *reader {
	index := len(exchanges) - 1
	for i := len(exchanges) - 1; i >= 0; i-- {
		if exchanges[i].reply != "" {
			index = i
			break
		}
	}
	return &reader{exchanges: exchanges, index: index, dark: dark, rendered: map[[3]int]string{}}
}

func (r *reader) Init() tea.Cmd { return nil }

func (r *reader) body() string {
	wrap := min(r.width-4, maxWidth)
	key := [3]int{r.index, wrap, 0}
	if r.showSent {
		key[2] = 1
	}
	if out, ok := r.rendered[key]; ok {
		return out
	}
	style := "light"
	if r.dark {
		style = "dark"
	}
	entry := r.exchanges[r.index]
	out := entry.reply
	if r.showSent || entry.reply == "" {
		var parts []string
		for _, sent := range entry.sent {
			parts = append(parts, "## You\n\n"+sent)
		}
		if entry.reply == "" {
			parts = append(parts, "*Waiting for a finished reply. Reopen Peek to refresh.*")
		} else {
			if len(entry.sent) == 0 {
				parts = append(parts, "*Sent messages unavailable for this reply.*")
			}
			parts = append(parts, "## Agent\n\n"+entry.reply)
		}
		out = strings.Join(parts, "\n\n---\n\n")
	}
	if renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(wrap)); err == nil {
		if md, err := renderer.Render(out); err == nil {
			out = md
		}
	}
	// Center the column on wide terminals.
	pad := max(0, (r.width-wrap-4)/2)
	out = lipgloss.NewStyle().PaddingLeft(pad).Render(out)
	r.rendered[key] = out
	return out
}

func (r *reader) show(index int) {
	if index < 0 || index >= len(r.exchanges) {
		return
	}
	r.index = index
	r.view.SetContent(r.body())
	r.view.GotoTop()
}

func (r *reader) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		r.width = msg.Width
		r.view = viewport.New(msg.Width, max(1, msg.Height-2))
		r.view.SetContent(r.body())
		return r, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "ctrl+\\", "ctrl+c":
			return r, tea.Quit
		case "[", "p":
			r.show(r.index - 1)
			return r, nil
		case "]", "n":
			r.show(r.index + 1)
			return r, nil
		case "s":
			r.showSent = !r.showSent
			r.show(r.index)
			return r, nil
		case "g", "home":
			r.view.GotoTop()
			return r, nil
		case "G", "end":
			r.view.GotoBottom()
			return r, nil
		case " ":
			r.view.PageDown()
			return r, nil
		}
	}
	var cmd tea.Cmd
	r.view, cmd = r.view.Update(msg)
	return r, cmd
}

func (r *reader) View() string {
	if r.width == 0 {
		return ""
	}
	dim := lipgloss.NewStyle().Faint(true)
	number, total := 0, 0
	for i, entry := range r.exchanges {
		if entry.reply != "" {
			total++
			if i <= r.index {
				number++
			}
		}
	}
	title := fmt.Sprintf(" Reply %d of %d", number, total)
	if r.exchanges[r.index].reply == "" {
		title = " Waiting for reply"
	}
	if r.view.TotalLineCount() > r.view.Height {
		title += fmt.Sprintf("  ·  %d%%", int(r.view.ScrollPercent()*100))
	}
	help := " esc back  ·  ↑↓ space scroll  ·  [ ] earlier/later"
	if r.exchanges[r.index].reply != "" {
		if r.showSent {
			help += "  ·  s hide sent"
		} else {
			help += "  ·  s show sent"
		}
	}
	return lipgloss.NewStyle().Bold(true).Render(title) + "\n" + r.view.View() + "\n" + dim.Render(help)
}

func runReader(exchanges []exchange) error {
	dark := darkMode()
	defer fmt.Print(paint(dark))
	_, err := tea.NewProgram(newReader(exchanges, dark), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}
