package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m *model) updateTunnelInputs(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = "table"
		m.tunnelLocalPort.Blur()
		m.tunnelRemoteHost.Blur()
		m.tunnelRemotePort.Blur()
		return m, nil
	case "tab", "shift+tab", "up", "down":
		s := msg.String()
		if s == "up" || s == "shift+tab" {
			m.tunnelFocusIndex--
		} else {
			m.tunnelFocusIndex++
		}

		if m.tunnelFocusIndex > 2 {
			m.tunnelFocusIndex = 0
		} else if m.tunnelFocusIndex < 0 {
			m.tunnelFocusIndex = 2
		}

		cmds := make([]tea.Cmd, 3)
		switch m.tunnelFocusIndex {
		case 0:
			cmds[0] = m.tunnelLocalPort.Focus()
			m.tunnelRemoteHost.Blur()
			m.tunnelRemotePort.Blur()
		case 1:
			m.tunnelLocalPort.Blur()
			cmds[1] = m.tunnelRemoteHost.Focus()
			m.tunnelRemotePort.Blur()
		default:
			m.tunnelLocalPort.Blur()
			m.tunnelRemoteHost.Blur()
			cmds[2] = m.tunnelRemotePort.Focus()
		}
		return m, tea.Batch(cmds...)
	case "enter":
		// Only submit if required fields are present
		lp := strings.TrimSpace(m.tunnelLocalPort.Value())
		rh := strings.TrimSpace(m.tunnelRemoteHost.Value())
		rp := strings.TrimSpace(m.tunnelRemotePort.Value())
		
		if lp != "" && rp != "" {
			if rh == "" {
				rh = "localhost"
			}
			m.tunnelArg = fmt.Sprintf("%s:%s:%s", lp, rh, rp)
			m.lastAction = actTunnel
			
			m.tunnelLocalPort.Blur()
			m.tunnelRemoteHost.Blur()
			m.tunnelRemotePort.Blur()
			
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch m.tunnelFocusIndex {
	case 0:
		m.tunnelLocalPort, cmd = m.tunnelLocalPort.Update(msg)
	case 1:
		m.tunnelRemoteHost, cmd = m.tunnelRemoteHost.Update(msg)
	default:
		m.tunnelRemotePort, cmd = m.tunnelRemotePort.Update(msg)
	}
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *model) viewTunnel(helpStyle lipgloss.Style) string {
	help := helpStyle.Render("tab/↑/↓: next input   enter: connect   esc: back   q: quit")

	titleText := "SSH Local Forward (-L):"
	row := m.tbl.Cursor()
	isK8s := false
	if row >= 0 && row < len(m.visible) {
		realIdx := m.visible[row]
		if m.recs[realIdx].Provider == "k8s" {
			titleText = "Kubernetes Port-Forward:"
			isK8s = true
		}
	}

	var inputs string
	if isK8s {
		// k8s only really needs Local Port and Remote Port (to the pod)
		inputs = fmt.Sprintf("Local Port: %s\nPod Port:   %s", 
			m.tunnelLocalPort.View(),
			m.tunnelRemotePort.View())
	} else {
		inputs = fmt.Sprintf("Local Port:  %s\nTarget Host: %s\nTarget Port: %s", 
			m.tunnelLocalPort.View(),
			m.tunnelRemoteHost.View(),
			m.tunnelRemotePort.View())
	}

	box := lipgloss.JoinVertical(
		lipgloss.Left,
		titleText,
		"",
		inputs,
	)
	
	return baseStyle.Render(box) + "\n" + help
}
