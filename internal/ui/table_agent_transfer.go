package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shareed2k/honey/internal/cloudtransfer"
	"github.com/shareed2k/honey/internal/hosts"
)

type agentTransferDoneMsg struct {
	title string
	body  string
	err   string
}

func (m *model) resetAgentTransfer() {
	m.agentPick = ""
	m.agentSrc = hosts.Record{}
	m.agentDst = hosts.Record{}
	m.agentFormStep = 0
	m.agentAwaitKeep = false
	m.agentKeepObject = false
	m.agentStorageIdx = 0
	for i := range m.agentFormValues {
		m.agentFormValues[i] = ""
	}
	m.ti.Blur()
}

func (m *model) recordAtCursor() (hosts.Record, bool) {
	row := m.tbl.Cursor()
	if row < 0 || row >= len(m.visible) {
		return hosts.Record{}, false
	}
	idx := m.visible[row]
	if idx < 0 || idx >= len(m.recs) {
		return hosts.Record{}, false
	}
	return m.recs[idx], true
}

func (m *model) agentPickEnter() (tea.Model, tea.Cmd) {
	rec, ok := m.recordAtCursor()
	if !ok || !HostConnectableForTransfer(rec) {
		return m, nil
	}
	switch m.agentPick {
	case "source":
		m.agentSrc = rec
		m.agentPick = "dest"
		return m, nil
	case "dest":
		m.agentDst = rec
		m.agentPick = ""
		m.mode = "agenttransferform"
		m.agentFormStep = 0
		m.agentAwaitKeep = false
		m.ti.Reset()
		m.ti.Placeholder = agentTransferPlaceholder(0)
		m.ti.Focus()
		return m, textinput.Blink
	default:
		return m, nil
	}
}

func (m *model) agentTransferFormLabel() string {
	switch m.agentFormStep {
	case 0:
		return "Source path (on source host)"
	case 1:
		return "Destination path (on destination host)"
	case 2:
		return "Cloud storage type"
	case 3:
		return "Bucket"
	case 4:
		return "Prefix (optional)"
	case 5:
		return "Region (optional)"
	case 6:
		return "Endpoint (optional)"
	case 7:
		return "Agent binary override (optional, empty = auto-build)"
	case 8:
		return "Max retries (default 2)"
	default:
		return ""
	}
}

func (m *model) advanceAgentTransferForm() (tea.Model, tea.Cmd) {
	val := strings.TrimSpace(m.ti.Value())
	switch m.agentFormStep {
	case 0, 1:
		if val == "" {
			return m, nil
		}
	case 3:
		if val == "" {
			return m, nil
		}
	case 8:
		if val != "" {
			if _, err := strconv.Atoi(val); err != nil {
				return m, nil
			}
		}
	}
	if m.agentFormStep >= 0 && m.agentFormStep < len(m.agentFormValues) {
		m.agentFormValues[m.agentFormStep] = val
	}
	m.agentFormStep++
	if m.agentFormStep > 8 {
		m.ti.Blur()
		m.agentAwaitKeep = true
		return m, nil
	}
	if m.agentFormStep == 2 {
		m.agentStorageIdx = 0
		m.ti.Blur()
		return m, nil
	}
	m.ti.Reset()
	m.ti.Placeholder = agentTransferPlaceholder(m.agentFormStep)
	m.ti.Focus()
	return m, textinput.Blink
}

func agentTransferPlaceholder(step int) string {
	switch step {
	case 0:
		return "/path/on/source"
	case 1:
		return "/path/on/destination"
	case 3:
		return "my-bucket"
	case 4:
		return "prefix (optional)"
	case 5:
		return "region (optional)"
	case 6:
		return "endpoint (optional)"
	case 7:
		return "override path or leave empty"
	case 8:
		return "2"
	default:
		return ""
	}
}

func (m *model) viewAgentTransferForm(helpStyle lipgloss.Style) string {
	title := lipgloss.NewStyle().Bold(true).Render("A → cloud → B (agent transfer)")
	if m.agentAwaitKeep {
		sub := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(
			"Keep staged object in cloud after successful download?  y = keep, n = delete (recommended)",
		)
		return title + "\n" + sub + "\n" + helpStyle.Render("esc: cancel   y/n")
	}
	line := m.agentTransferFormLabel()
	if m.agentFormStep == 2 {
		picker := m.viewAgentStoragePicker()
		return title + "\n" + helpStyle.Render(line) + "\n" + picker + "\n" +
			helpStyle.Render("↑/↓ j/k h/l tab: choose type   enter: confirm   esc: cancel")
	}
	return title + "\n" + helpStyle.Render(line) + "\n" + m.ti.View() + "\n" +
		helpStyle.Render("enter: next   esc: cancel   paste: "+pasteShortcutHelp())
}

func (m *model) viewAgentStoragePicker() string {
	opts := []struct {
		id   string
		name string
	}{
		{"s3", "Amazon S3 (provider id: s3)"},
		{"googlecloudstorage", "Google Cloud Storage (provider id: googlecloudstorage)"},
	}
	var b strings.Builder
	for i, o := range opts {
		cursor := "  "
		if i == m.agentStorageIdx {
			cursor = "> "
		}
		line := fmt.Sprintf("%s%s", cursor, o.name)
		if i == m.agentStorageIdx {
			line = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229")).Render(line)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *model) agentConfirmStorageAndAdvance() (tea.Model, tea.Cmd) {
	opts := []string{"s3", "googlecloudstorage"}
	m.agentFormValues[2] = opts[m.agentStorageIdx]
	m.agentFormStep++
	if m.agentFormStep > 8 {
		m.ti.Blur()
		m.agentAwaitKeep = true
		return m, nil
	}
	m.ti.Reset()
	m.ti.Placeholder = agentTransferPlaceholder(m.agentFormStep)
	m.ti.Focus()
	return m, textinput.Blink
}

func (m *model) updateAgentTransferFormKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.agentAwaitKeep {
		switch msg.String() {
		case "esc":
			m.resetAgentTransfer()
			m.mode = "table"
			return m, nil
		case "y", "Y":
			m.agentKeepObject = true
			m.agentAwaitKeep = false
			m.cueResultTitle = "Agent transfer"
			m.cueResultBody = "Running…\n"
			m.execResults = nil
			m.execScroll = 0
			m.mode = "execresults"
			return m, m.submitAgentTransferCmd()
		case "n", "N":
			m.agentKeepObject = false
			m.agentAwaitKeep = false
			m.cueResultTitle = "Agent transfer"
			m.cueResultBody = "Running…\n"
			m.execResults = nil
			m.execScroll = 0
			m.mode = "execresults"
			return m, m.submitAgentTransferCmd()
		default:
			return m, nil
		}
	}
	if m.agentFormStep == 2 {
		switch msg.String() {
		case "esc":
			m.resetAgentTransfer()
			m.mode = "table"
			return m, nil
		case "enter":
			return m.agentConfirmStorageAndAdvance()
		case "up", "k", "left", "h":
			m.agentStorageIdx = 0
			return m, nil
		case "down", "j", "right", "l", "tab":
			m.agentStorageIdx = 1
			return m, nil
		default:
			return m, nil
		}
	}
	switch msg.String() {
	case "esc":
		m.resetAgentTransfer()
		m.mode = "table"
		return m, nil
	case "enter":
		return m.advanceAgentTransferForm()
	default:
		if model, ok := m.tryClipboardPasteInTextInput(msg); ok {
			return model, nil
		}
		var cmd tea.Cmd
		m.ti, cmd = m.ti.Update(msg)
		return m, cmd
	}
}

func (m *model) submitAgentTransferCmd() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		maxR := 2
		if v := strings.TrimSpace(m.agentFormValues[8]); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				maxR = n
			}
		}
		cloud := AgentCloudBackend{
			Provider: m.agentFormValues[2],
			Bucket:   m.agentFormValues[3],
			Prefix:   m.agentFormValues[4],
			Region:   m.agentFormValues[5],
			Endpoint: m.agentFormValues[6],
		}
		events, err := RunAgentTransferWithFallback(
			ctx,
			m.fileClientCache,
			m.sshUser,
			strings.TrimSpace(m.agentFormValues[7]),
			"",
			"",
			"",
			m.agentSrc,
			m.agentDst,
			m.agentFormValues[0],
			m.agentFormValues[1],
			cloud,
			m.agentKeepObject,
			maxR,
			cloudtransfer.SigningHints{},
			LoadTransferConfigFromConfigPath(m.configPath),
			nil,
		)
		if err != nil {
			return agentTransferDoneMsg{title: "Agent transfer", body: formatAgentTransferEvents(events), err: err.Error()}
		}
		return agentTransferDoneMsg{title: "Agent transfer", body: formatAgentTransferEvents(events)}
	}
}

func formatAgentTransferEvents(events []AgentTransferEvent) string {
	var b strings.Builder
	for _, e := range events {
		host := e.Host
		if host == "" {
			host = "—"
		}
		ok := "ok"
		if !e.Success {
			ok = "FAIL"
		}
		msg := e.Message
		if e.Error != "" {
			if msg != "" {
				msg += " :: "
			}
			msg += e.Error
		}
		fmt.Fprintf(&b, "[%s] %s @ %s %s", e.Timestamp.UTC().Format(time.RFC3339), e.Stage, host, ok)
		if msg != "" {
			fmt.Fprintf(&b, " — %s", msg)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
