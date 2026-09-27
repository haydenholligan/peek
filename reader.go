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
	replies  []string // oldest first
	index    int
	dark     bool
	width    int
	view     viewport.Model
	rendered map[[2]int]string // (reply, width) -> rendered
}

func newReader(replies []string, dark bool) *reader {
	return &reader{replies: replies, index: len(replies) - 1, dark: dark, rendered: map[[2]int]string{}}
}

func (r *reader) Init() tea.Cmd { return nil }

func (r *reader) body() string {
	wrap := min(r.width-4, maxWidth)
	key := [2]int{r.index, wrap}
	if out, ok := r.rendered[key]; ok {
		return out
	}
	style := "light"
	if r.dark {
		style = "dark"
	}
	out := r.replies[r.index]
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
	if index < 0 || index >= len(r.replies) {
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
	title := fmt.Sprintf(" Reply %d of %d", r.index+1, len(r.replies))
	if r.view.TotalLineCount() > r.view.Height {
		title += fmt.Sprintf("  ·  %d%%", int(r.view.ScrollPercent()*100))
	}
	help := " esc back  ·  ↑↓ space scroll  ·  [ ] earlier/later reply"
	return lipgloss.NewStyle().Bold(true).Render(title) + "\n" + r.view.View() + "\n" + dim.Render(help)
}

func runReader(replies []string) error {
	dark := darkMode()
	defer fmt.Print(paint(dark))
	_, err := tea.NewProgram(newReader(replies, dark), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}
