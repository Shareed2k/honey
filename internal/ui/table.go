package ui

import (
	"bytes"
	"fmt"
	"honey/internal/cuetry"
	"honey/internal/hosts"
	"honey/internal/safepath"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type action int

const (
	actNone action = iota
	actSSH
	actTunnel
)

type parallelExecDoneMsg struct {
	results    []HostExecResult
	cmdLine    string
	targetNote string // how parallel exec scope was chosen (shown above results)
}

type cueRecipeDoneMsg struct {
	title string
	body  string
}

type model struct {
	recs    []hosts.Record
	tbl     table.Model
	ti      textinput.Model
	sshUser string
	mode    string // table | tunnel | execinput | execresults

	winW int
	winH int

	lastAction action
	tunnelArg  string

	// selected marks table row indices for parallel SSH (see execTargets).
	selected map[int]struct{}

	execResults    []HostExecResult
	execCmdLine    string
	execTargetNote string
	execScroll     int

	// CUE recipe output (non-empty body replaces SSH-style exec result lines).
	cueResultTitle string
	cueResultBody  string
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

func rowsFromRecs(recs []hosts.Record, selected map[int]struct{}) []table.Row {
	rows := make([]table.Row, 0, len(recs))
	for i, r := range recs {
		mark := " "
		if selected != nil {
			if _, ok := selected[i]; ok {
				mark = "*"
			}
		}
		reg := r.Region
		if reg == "" {
			reg = r.Meta["datacenter"]
		}
		rows = append(rows, table.Row{mark, r.Provider, r.Name, r.PrimaryIP, r.Zone, reg})
	}
	return rows
}

func newModel(records []hosts.Record, sshUser string) *model {
	sel := make(map[int]struct{})
	columns := []table.Column{
		{Title: "*", Width: 2},
		{Title: "Provider", Width: 8},
		{Title: "Name", Width: 26},
		{Title: "IP", Width: 16},
		{Title: "Zone", Width: 18},
		{Title: "Region/DC", Width: 14},
	}
	rows := rowsFromRecs(records, sel)
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
	ti.CharLimit = 400
	ti.Width = 60

	return &model{
		recs:     records,
		tbl:      t,
		ti:       ti,
		sshUser:  sshUser,
		mode:     "table",
		winW:     100,
		winH:     24,
		selected: sel,
	}
}

func (m *model) Init() tea.Cmd {
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case parallelExecDoneMsg:
		m.cueResultBody = ""
		m.cueResultTitle = "Parallel SSH results"
		m.execResults = SortHostExecForUI(msg.results)
		m.execCmdLine = msg.cmdLine
		m.execTargetNote = msg.targetNote
		m.execScroll = 0
		m.mode = "execresults"
		m.ti.Blur()
		return m, nil

	case cueRecipeDoneMsg:
		m.cueResultTitle = msg.title
		m.cueResultBody = msg.body
		m.execResults = nil
		m.execCmdLine = ""
		m.execTargetNote = ""
		m.execScroll = 0
		m.mode = "execresults"
		m.ti.Blur()
		return m, nil

	case tea.WindowSizeMsg:
		m.winW = msg.Width
		m.winH = msg.Height
		m.tbl.SetWidth(msg.Width - 4)
		m.tbl.SetHeight(msg.Height - 8)
		m.ti.Width = msg.Width - 8
		m.clampExecScroll()
		return m, nil

	case tea.KeyMsg:
		if m.mode == "execresults" {
			return m.updateExecResultsKeys(msg)
		}
		if m.mode == "tunnel" || m.mode == "execinput" || m.mode == "cueexecinput" {
			return m.updateTextInputMode(msg)
		}

		switch msg.String() {
		case "q", "ctrl+c":
			m.lastAction = actNone
			return m, tea.Quit
		case "x":
			m.toggleParallelMark()
			return m, nil
		case "ctrl+a":
			m.selectAllWithIPForParallel()
			return m, nil
		case "c":
			m.clearParallelMarks()
			return m, nil
		case "enter":
			m.lastAction = actSSH
			return m, tea.Quit
		case "t":
			m.mode = "tunnel"
			m.ti.Placeholder = "8080:localhost:8080 (local:remote for ssh -L)"
			m.ti.Reset()
			m.ti.Focus()
			return m, textinput.Blink
		case "e":
			m.mode = "execinput"
			m.ti.Placeholder = "remote shell command (* rows only, or all with IP if none marked)"
			m.ti.Reset()
			m.ti.Focus()
			return m, textinput.Blink
		case "r":
			m.mode = "cueexecinput"
			m.ti.Placeholder = "path/to/recipe.cue (! = execute) — * rows only, or all w/ IP if none marked"
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

func (m *model) updateTextInputMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = "table"
		m.ti.Blur()
		return m, nil
	case "enter":
		if m.mode == "tunnel" {
			m.tunnelArg = strings.TrimSpace(m.ti.Value())
			m.lastAction = actTunnel
			m.ti.Blur()
			return m, tea.Quit
		}
		if m.mode == "cueexecinput" {
			val := strings.TrimSpace(m.ti.Value())
			if val == "" {
				return m, nil
			}
			execute := false
			if strings.HasSuffix(val, "!") {
				execute = true
				val = strings.TrimSpace(strings.TrimSuffix(val, "!"))
			}
			if val == "" {
				return m, nil
			}
			targets, note := m.parallelExecTargets()
			m.ti.Blur()
			return m, runCueRecipeCmd(val, targets, note, m.sshUser, execute)
		}
		cmd := strings.TrimSpace(m.ti.Value())
		if cmd == "" {
			return m, nil
		}
		targets, note := m.parallelExecTargets()
		m.ti.Blur()
		return m, runParallelSSHCmd(m.sshUser, targets, cmd, note)
	}
	var cmd tea.Cmd
	m.ti, cmd = m.ti.Update(msg)
	return m, cmd
}

func (m *model) updateExecResultsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	lines := m.execResultLines()
	vis := m.visibleExecLines()
	maxScroll := 0
	if len(lines) > vis {
		maxScroll = len(lines) - vis
	}
	switch msg.String() {
	case "esc":
		m.mode = "table"
		m.execScroll = 0
		return m, nil
	case "q", "ctrl+c":
		m.lastAction = actNone
		return m, tea.Quit
	case "up", "k":
		if m.execScroll > 0 {
			m.execScroll--
		}
	case "down", "j":
		if m.execScroll < maxScroll {
			m.execScroll++
		}
	case "pgup", "b":
		m.execScroll -= vis / 2
		if m.execScroll < 0 {
			m.execScroll = 0
		}
	case "pgdown", "f":
		m.execScroll += vis / 2
		if m.execScroll > maxScroll {
			m.execScroll = maxScroll
		}
	case "home", "g":
		m.execScroll = 0
	case "end", "G":
		m.execScroll = maxScroll
	}
	return m, nil
}

func (m *model) clampExecScroll() {
	lines := m.execResultLines()
	vis := m.visibleExecLines()
	maxScroll := 0
	if len(lines) > vis {
		maxScroll = len(lines) - vis
	}
	if m.execScroll > maxScroll {
		m.execScroll = maxScroll
	}
	if m.execScroll < 0 {
		m.execScroll = 0
	}
}

func (m *model) visibleExecLines() int {
	h := m.winH - 7
	if h < 4 {
		h = 4
	}
	return h
}

func (m *model) execResultLines() []string {
	if strings.TrimSpace(m.cueResultBody) != "" {
		s := strings.TrimRight(m.cueResultBody, "\n")
		if s == "" {
			return []string{"(empty output)"}
		}
		return strings.Split(s, "\n")
	}
	var lines []string
	if m.execCmdLine != "" {
		lines = append(lines, fmt.Sprintf("Command: %s", m.execCmdLine))
		lines = append(lines, "")
	}
	if strings.TrimSpace(m.execTargetNote) != "" {
		lines = append(lines, m.execTargetNote)
		lines = append(lines, "")
	}
	if len(m.execResults) == 0 {
		lines = append(lines, "(no command output — no hosts ran or none with IP in scope)")
		return lines
	}
	for _, r := range m.execResults {
		status := "ok"
		if !r.Success {
			status = "FAILED"
		}
		head := fmt.Sprintf("[%s] %s @ %s — %s", r.Provider, r.Name, r.IP, status)
		if r.ErrMsg != "" {
			head += " — " + r.ErrMsg
		}
		lines = append(lines, head)
		if strings.TrimSpace(r.Output) != "" {
			for _, ln := range strings.Split(r.Output, "\n") {
				lines = append(lines, "    "+ln)
			}
		}
		lines = append(lines, "")
	}
	return lines
}

func (m *model) View() string {
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	switch m.mode {
	case "tunnel":
		help := helpStyle.Render("enter: connect   esc: back   q: quit")
		box := lipgloss.JoinVertical(
			lipgloss.Left,
			"SSH local forward (-L):",
			m.ti.View(),
		)
		return baseStyle.Render(box) + "\n" + help
	case "execinput":
		help := helpStyle.Render("enter: run   esc: back   q: quit")
		_, scope := m.parallelExecTargets()
		box := lipgloss.JoinVertical(
			lipgloss.Left,
			"Parallel SSH:",
			helpStyle.Render(scope),
			m.ti.View(),
		)
		return baseStyle.Render(box) + "\n" + help
	case "cueexecinput":
		help := helpStyle.Render("enter: run   esc: back   q: quit")
		_, scope := m.parallelExecTargets()
		box := lipgloss.JoinVertical(
			lipgloss.Left,
			"CUE recipe (selected hosts only):",
			helpStyle.Render(scope),
			m.ti.View(),
		)
		return baseStyle.Render(box) + "\n" + help
	case "execresults":
		lines := m.execResultLines()
		vis := m.visibleExecLines()
		start := m.execScroll
		end := start + vis
		if end > len(lines) {
			end = len(lines)
		}
		var body strings.Builder
		if len(lines) > 0 {
			body.WriteString(strings.Join(lines[start:end], "\n"))
		}
		scrollNote := ""
		if len(lines) > vis {
			scrollNote = fmt.Sprintf("lines %d–%d of %d", start+1, end, len(lines))
		}
		titleText := m.cueResultTitle
		if titleText == "" {
			titleText = "Parallel SSH results"
		}
		title := lipgloss.NewStyle().Bold(true).Render(titleText)
		help := helpStyle.Render("esc: table   q: quit   ↑/k ↓/j   pgup/pgdn   home/end")
		return title + "\n" + baseStyle.Width(m.winW-2).Render(body.String()) + "\n" +
			helpStyle.Render(scrollNote) + "\n" + help
	default:
		help := helpStyle.Render("enter: ssh   t: tunnel   e: parallel cmd   r: cue recipe   x: mark row   ^a: mark all w/ IP   c: clear marks   q: quit")
		nMark := len(m.selected)
		sub := ""
		if nMark > 0 {
			sub = helpStyle.Render(fmt.Sprintf("%d row(s) marked (* for parallel SSH and CUE recipe)", nMark)) + "\n"
		}
		title := lipgloss.NewStyle().Bold(true).Render("honey — select a host")
		return title + "\n" + sub + baseStyle.Render(m.tbl.View()) + "\n" + help
	}
}

func runCueRecipeCmd(recipePath string, targets []hosts.Record, targetNote string, sshUser string, execute bool) tea.Cmd {
	title := "CUE recipe (dry-run)"
	if execute {
		title = "CUE recipe (execute)"
	}
	return func() tea.Msg {
		if len(targets) == 0 {
			const noHostMsg = "(no hosts with IP in this scope — use x to mark rows, ^a to mark all with IP, or c to clear marks and use every row with an IP)"
			return cueRecipeDoneMsg{
				title: title,
				body:  targetNote + "\n\n" + noHostMsg,
			}
		}
		absRecipe, err := filepath.Abs(recipePath)
		if err != nil {
			return cueRecipeDoneMsg{title: title, body: targetNote + "\n\npath: " + err.Error()}
		}
		recipeDir := filepath.Dir(absRecipe)
		raw, err := safepath.ReadFile(absRecipe)
		if err != nil {
			return cueRecipeDoneMsg{title: title, body: targetNote + "\n\nread: " + err.Error()}
		}
		recipe, err := cuetry.ParseRemoteRecipe(raw)
		if err != nil {
			return cueRecipeDoneMsg{title: title, body: targetNote + "\n\nparse: " + err.Error()}
		}
		var buf bytes.Buffer
		runErr := RunCueRecipeSteps(&buf, recipe, recipeDir, targets, sshUser, execute, nil)
		body := targetNote + "\n\n" + buf.String()
		if runErr != nil {
			body += "\nError: " + runErr.Error()
		}
		return cueRecipeDoneMsg{title: title, body: body}
	}
}

func runParallelSSHCmd(user string, targets []hosts.Record, cmd, targetNote string) tea.Cmd {
	return func() tea.Msg {
		if len(targets) == 0 {
			return parallelExecDoneMsg{
				results:    []HostExecResult{},
				cmdLine:    cmd,
				targetNote: targetNote + " — nothing to run",
			}
		}
		res, err := ExecuteSSHParallel(user, targets, cmd, 0)
		if err != nil {
			res = []HostExecResult{{
				Name:     "(SSH setup)",
				Provider: "—",
				Success:  false,
				ErrMsg:   err.Error(),
			}}
		}
		if res == nil {
			res = []HostExecResult{}
		}
		return parallelExecDoneMsg{results: res, cmdLine: cmd, targetNote: targetNote}
	}
}

// parallelExecTargets returns hosts to run a parallel command on. If at least
// one table row is marked (*), only marked rows that have PrimaryIP are used.
// If nothing is marked, every row with PrimaryIP is used.
func (m *model) parallelExecTargets() ([]hosts.Record, string) {
	if len(m.selected) == 0 {
		var out []hosts.Record
		for _, r := range m.recs {
			if strings.TrimSpace(r.PrimaryIP) != "" {
				out = append(out, r)
			}
		}
		note := fmt.Sprintf("Scope: all %d host(s) with an IP (no * marks — use x on rows, or ^a to mark all with IP)", len(out))
		return out, note
	}
	var out []hosts.Record
	skippedNoIP := 0
	for i, r := range m.recs {
		if _, ok := m.selected[i]; !ok {
			continue
		}
		if strings.TrimSpace(r.PrimaryIP) == "" {
			skippedNoIP++
			continue
		}
		out = append(out, r)
	}
	note := fmt.Sprintf("Scope: %d host(s) from %d marked row(s)", len(out), len(m.selected))
	if skippedNoIP > 0 {
		note += fmt.Sprintf(" (%d marked row(s) have no IP)", skippedNoIP)
	}
	return out, note
}

func (m *model) refreshTableRows(preserveCursor int) {
	m.tbl.SetRows(rowsFromRecs(m.recs, m.selected))
	if preserveCursor >= 0 && preserveCursor < len(m.recs) {
		m.tbl.SetCursor(preserveCursor)
	}
}

func (m *model) toggleParallelMark() {
	cur := m.tbl.Cursor()
	if cur < 0 || cur >= len(m.recs) {
		return
	}
	if _, ok := m.selected[cur]; ok {
		delete(m.selected, cur)
	} else {
		m.selected[cur] = struct{}{}
	}
	m.refreshTableRows(cur)
}

func (m *model) selectAllWithIPForParallel() {
	m.selected = make(map[int]struct{})
	for i, r := range m.recs {
		if strings.TrimSpace(r.PrimaryIP) != "" {
			m.selected[i] = struct{}{}
		}
	}
	m.refreshTableRows(m.tbl.Cursor())
}

func (m *model) clearParallelMarks() {
	m.selected = make(map[int]struct{})
	m.refreshTableRows(m.tbl.Cursor())
}

func runSSH(user, host string) error {
	if host == "" {
		return fmt.Errorf("no IP for selected host")
	}
	return runSSHInteractive(user, host)
}

func runTunnel(user, host, localFwd string) error {
	if host == "" {
		return fmt.Errorf("no IP for selected host")
	}
	if localFwd == "" || !strings.Contains(localFwd, ":") {
		return fmt.Errorf("tunnel spec must look like 8080:remotehost:8080")
	}
	return runTunnelGo(user, host, localFwd)
}
