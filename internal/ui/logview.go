package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shareed2k/honey/internal/hosts"
)

type logLineMsg string

// LogViewModel is the Bubble Tea model for the log viewer.
type LogViewModel struct {
	mu            sync.Mutex
	allLines      []string
	bufferedLines []string // Lines received while paused
	paused        bool
	scroll        int
	winW, winH    int
	opts          LogOptions
	logChan       chan string
	statusMsg     string
}

// NewLogViewModel creates a new log view model.
func NewLogViewModel(opts LogOptions) *LogViewModel {
	return &LogViewModel{
		opts:    opts,
		logChan: make(chan string, 1000),
	}
}

// Init initializes the log view model.
func (m *LogViewModel) Init() tea.Cmd {
	return m.waitForLine()
}

func (m *LogViewModel) waitForLine() tea.Cmd {
	return func() tea.Msg {
		line, ok := <-m.logChan
		if !ok {
			return nil
		}
		return logLineMsg(line)
	}
}

// Update handles messages and updates the log view model.
func (m *LogViewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.winW = msg.Width
		m.winH = msg.Height
		return m, nil

	case tea.KeyMsg:
		m.statusMsg = "" // Clear status on interaction
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case " ":
			m.paused = !m.paused
			if !m.paused {
				m.mu.Lock()
				m.allLines = append(m.allLines, m.bufferedLines...)
				m.bufferedLines = nil
				m.mu.Unlock()
				m.scrollToBottom()
			}
			return m, nil
		case "up", "k":
			m.scroll--
			if m.scroll < 0 {
				m.scroll = 0
			}
			return m, nil
		case "down", "j":
			m.scroll++
			m.fixScroll()
			return m, nil
		case "pgup":
			m.scroll -= m.winH - 4
			if m.scroll < 0 {
				m.scroll = 0
			}
			return m, nil
		case "pgdn":
			m.scroll += m.winH - 4
			m.fixScroll()
			return m, nil
		case "home":
			m.scroll = 0
			return m, nil
		case "end":
			m.scrollToBottom()
			return m, nil
		case "s":
			m.saveToFile()
			return m, nil
		}

	case logLineMsg:
		m.mu.Lock()
		if m.paused {
			m.bufferedLines = append(m.bufferedLines, string(msg))
		} else {
			m.allLines = append(m.allLines, string(msg))
			m.scrollToBottom()
		}
		m.mu.Unlock()
		return m, m.waitForLine()
	}

	return m, nil
}

func (m *LogViewModel) scrollToBottom() {
	vis := m.winH - 4
	if len(m.allLines) > vis {
		m.scroll = len(m.allLines) - vis
	} else {
		m.scroll = 0
	}
}

func (m *LogViewModel) fixScroll() {
	vis := m.winH - 4
	maxScroll := len(m.allLines) - vis
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
}

// View renders the log viewer UI.
func (m *LogViewModel) View() tea.View {
	if m.winW == 0 || m.winH == 0 {
		return tea.NewView("Initializing...")
	}

	header := lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("Logs: %s", m.opts.Target))
	if m.paused {
		header += lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Render(" [PAUSED]")
	}
	if len(m.bufferedLines) > 0 {
		header += fmt.Sprintf(" (%d lines buffered)", len(m.bufferedLines))
	}

	vis := m.winH - 4
	if vis < 0 {
		vis = 0
	}

	var body strings.Builder
	m.mu.Lock()
	end := m.scroll + vis
	if end > len(m.allLines) {
		end = len(m.allLines)
	}
	if m.scroll < len(m.allLines) {
		for _, line := range m.allLines[m.scroll:end] {
			body.WriteString(line + "\n")
		}
	}
	m.mu.Unlock()

	footer := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(
		"space: pause/resume   s: save   q: quit   arrows/pgup/pgdn: scroll",
	)

	if m.statusMsg != "" {
		footer = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true).Render(m.statusMsg) + "   " + footer
	}

	view := tea.NewView(header + "\n\n" + body.String() + "\n" + footer)
	view.AltScreen = true
	return view
}

func (m *LogViewModel) saveToFile() {
	dir := "logs"
	_ = os.MkdirAll(dir, 0o750) // #nosec G301 -- logs directory permissions
	filename := filepath.Join(dir, fmt.Sprintf("logs-%s-%s.log", m.opts.Target, time.Now().Format("20060102-150405")))

	m.mu.Lock()
	content := strings.Join(m.allLines, "\n")
	m.mu.Unlock()

	err := os.WriteFile(filename, []byte(content), 0o600) // #nosec G306 -- log file permissions
	if err != nil {
		m.statusMsg = fmt.Sprintf("Error saving: %v", err)
	} else {
		m.statusMsg = fmt.Sprintf("Saved to %s", filename)
	}
}

type uiWriter struct {
	ch chan<- string
}

func (w *uiWriter) Write(p []byte) (int, error) {
	// uiWriter expects lines without the trailing newline because prefixedWriter might have stripped it or we handle it in View
	// Actually, prefixedWriter uses fmt.Fprintln which adds \n.
	// We'll strip it here so our View can join with \n.
	s := strings.TrimRight(string(p), "\n")
	w.ch <- s
	return len(p), nil
}

// RunLogTUI starts the interactive log viewer.
func RunLogTUI(ctx context.Context, user string, records []hosts.Record, opts LogOptions, cache *ClientCache) error {
	m := NewLogViewModel(opts)

	// Create a sub-context so we can cancel log streaming when TUI exits
	logCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		writer := &uiWriter{ch: m.logChan}
		err := StreamLogs(logCtx, user, records, opts, cache, writer)
		if err != nil && err != context.Canceled {
			m.logChan <- fmt.Sprintf("Error streaming logs: %v", err)
		}
		close(m.logChan)
	}()

	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}
