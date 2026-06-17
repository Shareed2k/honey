package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/shareed2k/honey/internal/hosts"
)

// TunnelResult wraps the tunnel creation outcome.
type TunnelResult struct {
	Arg      string
	Canceled bool
}

// TunnelModel is the BubbleTea sub-model for the tunnel popup.
type TunnelModel struct {
	localPort  textinput.Model
	remoteHost textinput.Model
	remotePort textinput.Model
	focusIndex int
	isK8s      bool
	Record     hosts.Record
	Width      int
	Height     int
}

// NewTunnelModel initializes the tunnel sub-model.
func NewTunnelModel(r hosts.Record, isK8s bool, width, height int) *TunnelModel {
	m := &TunnelModel{
		isK8s:  isK8s,
		Record: r,
		Width:  width,
		Height: height,
	}
	m.localPort = textinput.New()
	m.localPort.Placeholder = "8080"
	m.localPort.Focus()

	m.remoteHost = textinput.New()
	m.remoteHost.Placeholder = "localhost"
	if isK8s {
		m.remoteHost.SetValue("localhost")
	}

	m.remotePort = textinput.New()
	m.remotePort.Placeholder = "80"

	return m
}

// Init satisfies tea.Model.
func (m *TunnelModel) Init() tea.Cmd {
	return textinput.Blink
}

// Update satisfies tea.Model.
func (m *TunnelModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return TunnelResult{Canceled: true} }
		case "tab", "shift+tab", "up", "down":
			s := msg.String()
			if s == "up" || s == "shift+tab" {
				m.focusIndex--
			} else {
				m.focusIndex++
			}

			if m.isK8s && m.focusIndex == 1 {
				if s == "up" || s == "shift+tab" {
					m.focusIndex--
				} else {
					m.focusIndex++
				}
			}

			if m.focusIndex > 2 {
				m.focusIndex = 0
			} else if m.focusIndex < 0 {
				m.focusIndex = 2
			}

			cmds := make([]tea.Cmd, 3)
			switch m.focusIndex {
			case 0:
				cmds[0] = m.localPort.Focus()
				m.remoteHost.Blur()
				m.remotePort.Blur()
			case 1:
				m.localPort.Blur()
				cmds[1] = m.remoteHost.Focus()
				m.remotePort.Blur()
			default:
				m.localPort.Blur()
				m.remoteHost.Blur()
				cmds[2] = m.remotePort.Focus()
			}
			return m, tea.Batch(cmds...)
		case "enter":
			lp := strings.TrimSpace(m.localPort.Value())
			rh := strings.TrimSpace(m.remoteHost.Value())
			rp := strings.TrimSpace(m.remotePort.Value())
			if lp == "" {
				lp = "8080"
			}
			if rp == "" {
				rp = "80"
			}
			var arg string
			if m.isK8s {
				arg = fmt.Sprintf("%s:%s", lp, rp)
			} else {
				if rh == "" {
					rh = "localhost"
				}
				arg = fmt.Sprintf("%s:%s:%s", lp, rh, rp)
			}
			return m, func() tea.Msg { return TunnelResult{Arg: arg, Canceled: false} }
		}
	}

	var cmd tea.Cmd
	switch m.focusIndex {
	case 0:
		m.localPort, cmd = m.localPort.Update(msg)
	case 1:
		m.remoteHost, cmd = m.remoteHost.Update(msg)
	default:
		m.remotePort, cmd = m.remotePort.Update(msg)
	}
	return m, cmd
}

// View satisfies tea.Model.
func (m *TunnelModel) View() tea.View {
	b := &strings.Builder{}
	fmt.Fprintf(b, "Create Port Forward (Local \u2192 Remote)\n\n")

	fmt.Fprintf(b, "Local Port:\n%s\n\n", m.localPort.View())
	if m.isK8s {
		fmt.Fprintf(b, "Target Host: (ignored for k8s pod)\n\n")
	} else {
		fmt.Fprintf(b, "Target Host (from %s's perspective):\n%s\n\n", m.Record.Name, m.remoteHost.View())
	}
	fmt.Fprintf(b, "Target Port:\n%s\n\n", m.remotePort.View())

	fmt.Fprintf(b, "(esc to cancel, enter to start, tab to switch)")

	content := b.String()
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(1, 3).
		Render(content)

	return tea.NewView(lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center, box))
}
