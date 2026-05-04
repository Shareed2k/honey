package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

func (m *model) viewExecResults(helpStyle lipgloss.Style) string {
	var body strings.Builder

	if strings.TrimSpace(m.cueResultBody) != "" {
		return m.viewCueResults(helpStyle, &body)
	}

	if m.execPopupOpen {
		return m.viewExecPopup(helpStyle, &body)
	}

	return m.viewExecList(helpStyle, &body)
}

func (m *model) viewCueResults(helpStyle lipgloss.Style, body *strings.Builder) string {
	s := strings.TrimRight(m.cueResultBody, "\n")
	lines := []string{"(empty output)"}
	if s != "" {
		lines = strings.Split(s, "\n")
	}
	vis := m.visibleExecLines()
	start := m.execScroll
	end := start + vis
	if end > len(lines) {
		end = len(lines)
	}
	if len(lines) > 0 {
		body.WriteString(strings.Join(lines[start:end], "\n"))
	}
	scrollNote := ""
	if len(lines) > vis {
		scrollNote = fmt.Sprintf("lines %d–%d of %d", start+1, end, len(lines))
	}
	titleText := m.cueResultTitle
	if titleText == "" {
		titleText = "CUE recipe results"
	}
	title := lipgloss.NewStyle().Bold(true).Render(titleText)
	help := helpStyle.Render("esc: table   q: quit   ↑/k ↓/j   pgup/pgdn   home/end")
	return title + "\n" + baseStyle.Width(m.winW-2).Render(body.String()) + "\n" +
		helpStyle.Render(scrollNote) + "\n" + help
}

func (m *model) viewExecPopup(helpStyle lipgloss.Style, body *strings.Builder) string {
	lines := m.popupResultLines()
	vis := m.visibleExecLines()
	start := m.execPopupScroll
	end := start + vis
	if end > len(lines) {
		end = len(lines)
	}
	if len(lines) > 0 {
		body.WriteString(strings.Join(lines[start:end], "\n"))
	}
	scrollNote := ""
	if len(lines) > vis {
		scrollNote = fmt.Sprintf("lines %d–%d of %d", start+1, end, len(lines))
	}
	titleText := "Host Execution Detail"
	title := lipgloss.NewStyle().Bold(true).Render(titleText)
	help := helpStyle.Render("esc/enter: back to list   q: quit   ↑/k ↓/j   pgup/pgdn   home/end")
	return title + "\n" + baseStyle.Width(m.winW-2).Render(body.String()) + "\n" +
		helpStyle.Render(scrollNote) + "\n" + help
}

func (m *model) viewExecList(helpStyle lipgloss.Style, body *strings.Builder) string {
	vis := m.visibleExecLines()
	start := m.execScroll
	end := start + vis
	if end > len(m.execResults) {
		end = len(m.execResults)
	}

	if len(m.execResults) == 0 {
		if !m.execDone {
			fmt.Fprintf(body, "Running... 0 / %d\n", m.execTotalJobs)
		} else {
			body.WriteString("(no command output — no hosts ran or none with IP in scope)\n")
		}
	} else {
		if !m.execDone {
			fmt.Fprintf(body, "Running... %d / %d\n\n", len(m.execResults), m.execTotalJobs)
			vis -= 2 // Account for running header
			// Recompute end with smaller vis
			end = start + vis
			if end > len(m.execResults) {
				end = len(m.execResults)
			}
		}

		for i := start; i < end; i++ {
			r := m.execResults[i]
			cursor := "  "
			if i == m.execListCursor {
				cursor = "> "
			}
			status := "ok"
			statusColor := lipgloss.Color("42") // green
			if !r.Success {
				status = "FAILED"
				statusColor = lipgloss.Color("196") // red
			}

			styledStatus := lipgloss.NewStyle().Foreground(statusColor).Render(status)
			row := fmt.Sprintf("%s[%s] %s @ %s — %s", cursor, r.Provider, r.Name, r.IP, styledStatus)

			if r.ErrMsg != "" {
				row += " — " + r.ErrMsg
			}

			if i == m.execListCursor {
				row = lipgloss.NewStyle().Bold(true).Render(row)
			}
			body.WriteString(row + "\n")
		}
	}

	scrollNote := ""
	if len(m.execResults) > vis {
		scrollNote = fmt.Sprintf("items %d–%d of %d", start+1, end, len(m.execResults))
	}
	titleText := m.cueResultTitle
	if titleText == "" {
		titleText = "Parallel SSH results"
		if !m.execDone {
			titleText += " (Running...)"
		}
	}
	title := lipgloss.NewStyle().Bold(true).Render(titleText)

	help := helpStyle.Render("enter: view output   esc: table   q: quit   ↑/k ↓/j   pgup/pgdn   home/end")
	if !m.execDone {
		help = helpStyle.Render("Running... please wait.   esc: cancel/back   q: quit")
	}
	return title + "\n" + baseStyle.Width(m.winW-2).Render(body.String()) + "\n" +
		helpStyle.Render(scrollNote) + "\n" + help
}
