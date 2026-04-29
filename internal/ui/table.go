package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"hostctl/internal/hosts"
)

type action int

const (
	actNone action = iota
	actSSH
	actTunnel
)

type model struct {
	recs       []hosts.Record
	tbl        table.Model
	ti         textinput.Model
	sshUser    string
	mode       string // table | tunnel
	lastAction action
	tunnelArg  string
}

var baseStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.NormalBorder()).
	BorderForeground(lipgloss.Color("240"))

// RunTable shows an interactive table and optionally execs ssh after the UI exits.
func RunTable(records []hosts.Record, sshUser string) error {
	if len(records) == 0 {
		fmt.Fprintln(os.Stderr, "no matching hosts")
		return nil
	}
	m := newModel(records, sshUser)
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return err
	}
	fm, ok := final.(*model)
	if !ok || fm == nil {
		return nil
	}
	row := fm.tbl.Cursor()
	if row < 0 || row >= len(fm.recs) {
		return nil
	}
	r := fm.recs[row]
	switch fm.lastAction {
	case actSSH:
		return runSSH(fm.sshUser, r.PrimaryIP)
	case actTunnel:
		return runTunnel(fm.sshUser, r.PrimaryIP, fm.tunnelArg)
	default:
		return nil
	}
}

func newModel(records []hosts.Record, sshUser string) *model {
	columns := []table.Column{
		{Title: "Provider", Width: 8},
		{Title: "Name", Width: 28},
		{Title: "IP", Width: 16},
		{Title: "Zone", Width: 18},
		{Title: "Region/DC", Width: 14},
	}
	var rows []table.Row
	for _, r := range records {
		reg := r.Region
		if reg == "" {
			reg = r.Meta["datacenter"]
		}
		rows = append(rows, table.Row{r.Provider, r.Name, r.PrimaryIP, r.Zone, reg})
	}
	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(12),
	)
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(false)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	t.SetStyles(s)

	ti := textinput.New()
	ti.Placeholder = "8080:localhost:8080 (local:remote for ssh -L)"
	ti.CharLimit = 120
	ti.Width = 60

	return &model{
		recs:    records,
		tbl:     t,
		ti:      ti,
		sshUser: sshUser,
		mode:    "table",
	}
}

func (m *model) Init() tea.Cmd {
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.tbl.SetWidth(msg.Width - 4)
		m.tbl.SetHeight(msg.Height - 8)
		m.ti.Width = msg.Width - 8
		return m, nil

	case tea.KeyMsg:
		if m.mode == "tunnel" {
			switch msg.String() {
			case "esc":
				m.mode = "table"
				m.ti.Blur()
				return m, nil
			case "enter":
				m.tunnelArg = strings.TrimSpace(m.ti.Value())
				m.lastAction = actTunnel
				m.ti.Blur()
				return m, tea.Quit
			}
			var cmd tea.Cmd
			m.ti, cmd = m.ti.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "q", "ctrl+c":
			m.lastAction = actNone
			return m, tea.Quit
		case "enter":
			m.lastAction = actSSH
			return m, tea.Quit
		case "t":
			m.mode = "tunnel"
			m.ti.Reset()
			m.ti.Focus()
			return m, textinput.Blink
		}
		var cmd tea.Cmd
		m.tbl, cmd = m.tbl.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.tbl, cmd = m.tbl.Update(msg)
	return m, cmd
}

func (m *model) View() string {
	help := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(
		"enter: ssh   t: tunnel (-L spec)   q: quit",
	)
	if m.mode == "tunnel" {
		box := lipgloss.JoinVertical(
			lipgloss.Left,
			"SSH local forward (-L):",
			m.ti.View(),
			"(enter to connect, esc back)",
		)
		return baseStyle.Render(box) + "\n" + help
	}
	title := lipgloss.NewStyle().Bold(true).Render("hostctl — select a host")
	return title + "\n" + baseStyle.Render(m.tbl.View()) + "\n" + help
}

func runSSH(user, host string) error {
	if host == "" {
		return fmt.Errorf("no IP for selected host")
	}
	target := fmt.Sprintf("%s@%s", user, host)
	cmd := exec.Command("ssh", target)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runTunnel(user, host, localFwd string) error {
	if host == "" {
		return fmt.Errorf("no IP for selected host")
	}
	if localFwd == "" || !strings.Contains(localFwd, ":") {
		return fmt.Errorf("tunnel spec must look like 8080:remotehost:8080")
	}
	target := fmt.Sprintf("%s@%s", user, host)
	cmd := exec.Command("ssh", "-L", localFwd, target)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
