package ui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shareed2k/honey/internal/recordings"
)

type replayTickMsg struct{}

type replayModel struct {
	fileName   string
	events     []recordings.Event
	structured bool
	loadErr    error

	winW int
	winH int

	body      strings.Builder
	nextEvent int
	elapsedMs float64
	speed     float64
	paused    bool
	playing   bool
	done      bool

	scrollLine int
}

var ansiEscapes = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func stripAnsiReplay(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return ansiEscapes.ReplaceAllString(s, "")
}

func formatHostExecReplay(r HostExecResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n── %s (%s)  ip=%s  ok=%v  exit=%d\n", r.Name, r.Provider, r.IP, r.Success, r.ExitCode)
	if r.ErrMsg != "" {
		fmt.Fprintf(&b, "err: %s\n", stripAnsiReplay(r.ErrMsg))
	}
	if r.Output != "" {
		b.WriteString(stripAnsiReplay(r.Output))
		b.WriteString("\n")
	}
	return b.String()
}

func (m *replayModel) writeBanner() {
	m.body.Reset()
	fmt.Fprintf(&m.body, "Replay: %s\n", m.fileName)
	for _, e := range m.events {
		if e.Type == "open" && e.Message != "" {
			m.body.WriteString("[open] ")
			m.body.WriteString(stripAnsiReplay(e.Message))
			m.body.WriteString("\n\n")
			return
		}
	}
	m.body.WriteString("\n")
}

func (m *replayModel) applyEvent(ev recordings.Event) {
	switch ev.Type {
	case "open":
		return
	case "close":
		m.body.WriteString("\n[closed]\n")
	case "error":
		if ev.Message != "" {
			m.body.WriteString("\n[error] ")
			m.body.WriteString(stripAnsiReplay(ev.Message))
			m.body.WriteString("\n")
		}
	case "resize":
		// Recorded on SIGWINCH during capture; we are not emulating a resizable terminal here.
		// Omit from TUI text (web TTY replay also does not print resize to xterm).
		return
	case "result":
		if len(ev.Result) == 0 {
			return
		}
		var r HostExecResult
		if err := json.Unmarshal(ev.Result, &r); err != nil {
			return
		}
		m.body.WriteString(formatHostExecReplay(r))
	case "data":
		if ev.DataB64 == "" {
			return
		}
		raw, err := base64.StdEncoding.DecodeString(ev.DataB64)
		if err != nil {
			return
		}
		text := string(raw)
		switch ev.Direction {
		case "plan":
			m.body.WriteString("\n[plan]\n")
			m.body.WriteString(stripAnsiReplay(text))
			m.body.WriteString("\n")
		case "stdout", "stderr":
			m.body.WriteString(stripAnsiReplay(text))
		}
	}
}

func (m *replayModel) visibleBodyLines() int {
	h := m.winH - 6
	if h < 4 {
		h = 4
	}
	return h
}

func (m *replayModel) clampScroll() {
	lines := strings.Split(m.body.String(), "\n")
	maxScroll := len(lines) - m.visibleBodyLines()
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scrollLine > maxScroll {
		m.scrollLine = maxScroll
	}
	if m.scrollLine < 0 {
		m.scrollLine = 0
	}
}

func (m *replayModel) autoScrollToEnd() {
	lines := strings.Split(m.body.String(), "\n")
	vis := m.visibleBodyLines()
	m.scrollLine = len(lines) - vis
	if m.scrollLine < 0 {
		m.scrollLine = 0
	}
}

// RunRecordingReplay plays one .hrec.jsonl in a nested full-screen Bubble Tea view.
func RunRecordingReplay(recordDir, baseName string) error {
	m := &replayModel{
		fileName: baseName,
		speed:    1.0,
		winW:     80,
		winH:     24,
	}
	evts, err := recordings.LoadEvents(recordDir, baseName)
	if err != nil {
		m.loadErr = err
	} else {
		m.events = evts
		m.structured = recordings.HasStructuredBatch(evts)
		if len(evts) == 0 {
			m.body.WriteString("(empty recording)\n")
		} else {
			m.writeBanner()
			m.playing = true
		}
	}
	_, err = tea.NewProgram(m).Run()
	return err
}

func (m *replayModel) Init() tea.Cmd {
	if m.loadErr != nil {
		return nil
	}
	if !m.playing {
		return nil
	}
	return tea.Tick(30*time.Millisecond, func(time.Time) tea.Msg { return replayTickMsg{} })
}

func (m *replayModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.winW = msg.Width
		m.winH = msg.Height
		m.clampScroll()
		return m, nil

	case replayTickMsg:
		if m.loadErr != nil {
			return m, nil
		}
		if m.done {
			return m, nil
		}
		if !m.playing || m.paused {
			return m, tea.Tick(30*time.Millisecond, func(time.Time) tea.Msg { return replayTickMsg{} })
		}
		m.elapsedMs += 30.0 * m.speed
		for m.nextEvent < len(m.events) && float64(m.events[m.nextEvent].TimeMS) <= m.elapsedMs {
			m.applyEvent(m.events[m.nextEvent])
			m.nextEvent++
		}
		if m.nextEvent >= len(m.events) {
			m.done = true
			m.playing = false
		}
		m.autoScrollToEnd()
		m.clampScroll()
		return m, tea.Tick(30*time.Millisecond, func(time.Time) tea.Msg { return replayTickMsg{} })

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case " ":
			if m.loadErr == nil && len(m.events) > 0 {
				m.paused = !m.paused
			}
			return m, nil
		case "+", "=":
			m.speed = math.Min(8, m.speed*1.5)
			return m, nil
		case "-", "_":
			m.speed = math.Max(0.25, m.speed/1.5)
			return m, nil
		case "up", "k":
			if m.scrollLine > 0 {
				m.scrollLine--
			}
			return m, nil
		case "down", "j":
			lines := strings.Split(m.body.String(), "\n")
			maxScroll := len(lines) - m.visibleBodyLines()
			if maxScroll < 0 {
				maxScroll = 0
			}
			if m.scrollLine < maxScroll {
				m.scrollLine++
			}
			return m, nil
		case "pgup", "b":
			m.scrollLine -= m.visibleBodyLines() / 2
			if m.scrollLine < 0 {
				m.scrollLine = 0
			}
			return m, nil
		case "pgdown", "f":
			m.scrollLine += m.visibleBodyLines() / 2
			m.clampScroll()
			return m, nil
		case "home", "g":
			m.scrollLine = 0
			return m, nil
		case "end", "G":
			lines := strings.Split(m.body.String(), "\n")
			m.scrollLine = len(lines) - m.visibleBodyLines()
			if m.scrollLine < 0 {
				m.scrollLine = 0
			}
			return m, nil
		}
		return m, nil
	}
	return m, nil
}

func (m *replayModel) View() tea.View {
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	if m.loadErr != nil {
		box := lipgloss.JoinVertical(
			lipgloss.Left,
			lipgloss.NewStyle().Bold(true).Render("Recording replay"),
			"",
			fmt.Sprintf("Error: %v", m.loadErr),
			"",
			helpStyle.Render("q / esc: close"),
		)
		v := tea.NewView(box)
		v.AltScreen = true
		return v
	}

	state := "playing"
	if m.done {
		state = "done"
	} else if m.paused {
		state = "paused"
	}
	help := helpStyle.Render(fmt.Sprintf(
		"space: pause/resume   +/-: speed (%.2gx)   %s   pgup/pgdn: scroll   home/end: jump   q/esc: close",
		m.speed,
		state,
	))

	lines := strings.Split(m.body.String(), "\n")
	vis := m.visibleBodyLines()
	if m.scrollLine+vis > len(lines) {
		m.scrollLine = len(lines) - vis
		if m.scrollLine < 0 {
			m.scrollLine = 0
		}
	}
	end := m.scrollLine + vis
	if end > len(lines) {
		end = len(lines)
	}
	slice := lines[m.scrollLine:end]
	body := strings.Join(slice, "\n")
	kind := lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render("  (TTY; ANSI stripped in TUI)")
	if m.structured {
		kind = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render("  (structured)")
	}
	title := lipgloss.JoinHorizontal(lipgloss.Left, lipgloss.NewStyle().Bold(true).Render("Recording replay — "+m.fileName), kind)

	box := lipgloss.JoinVertical(lipgloss.Left, title, "", body, "", help)
	v := tea.NewView(box)
	v.AltScreen = true
	return v
}
