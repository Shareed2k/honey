package ui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/safepath"
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
	mode    string // table | tunnel | execinput | execresults | filter
	filter  string
	visible []int // indexes of recs visible when filtered

	winW int
	winH int

	lastAction action
	tunnelArg  string

	// selected marks table row indices for parallel SSH (see execTargets).
	selected map[int]struct{}

	execResults     []HostExecResult
	execCmdLine     string
	execTargetNote  string
	execScroll      int // Scroll position for the list view
	execTotalJobs   int
	execDone        bool
	execListCursor  int // Selected item in the results list
	execPopupOpen   bool
	execPopupScroll int // Scroll position inside the popup

	// CUE recipe output (non-empty body replaces SSH-style exec result lines).
	cueResultTitle string
	cueResultBody  string

	// CUE dropdown
	availableRecipes []string
	recipeCursor     int
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
		return runSSH(fm.sshUser, r)
	case actTunnel:
		return runTunnel(fm.sshUser, r.PrimaryIP, fm.tunnelArg)
	default:
		return nil
	}
}

func rowsFromRecs(recs []hosts.Record, visible []int, selected map[int]struct{}) []table.Row {
	rows := make([]table.Row, 0, len(visible))
	for _, idx := range visible {
		r := recs[idx]
		mark := " "
		if selected != nil {
			if _, ok := selected[idx]; ok {
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
	vis := make([]int, len(records))
	for i := range records {
		vis[i] = i
	}

	columns := []table.Column{
		{Title: "*", Width: 2},
		{Title: "Provider", Width: 8},
		{Title: "Name", Width: 26},
		{Title: "IP", Width: 16},
		{Title: "Zone", Width: 18},
		{Title: "Region/DC", Width: 14},
	}
	rows := rowsFromRecs(records, vis, sel)
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
		recs:             records,
		tbl:              t,
		ti:               ti,
		sshUser:          sshUser,
		mode:             "table",
		visible:          vis,
		winW:             100,
		winH:             24,
		selected:         sel,
		availableRecipes: config.ListDefaultRecipes(),
	}
}

func (m *model) Init() tea.Cmd {
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case streamStartMsg:
		m.mode = "execresults"
		m.execCmdLine = msg.cmdLine
		m.execTargetNote = msg.targetNote
		m.execResults = nil
		m.execScroll = 0
		m.execListCursor = 0
		m.execPopupOpen = false
		m.execPopupScroll = 0
		m.execTotalJobs = msg.totalJobs
		m.execDone = false
		m.cueResultBody = ""
		if msg.isCue {
			m.cueResultTitle = "CUE recipe execution"
		} else {
			m.cueResultTitle = ""
		}
		return m, readNextStreamResult(msg.ch)

	case streamResultMsg:
		m.execResults = append(m.execResults, msg.res)

		// If cursor is at the bottom and a new item arrives, follow it if we are at the end
		if !m.execPopupOpen && m.execListCursor == len(m.execResults)-2 {
			m.execListCursor = len(m.execResults) - 1

			// Adjust scroll to keep it in view
			vis := m.visibleExecLines() - len(m.execResultLines())
			if vis < 1 {
				vis = 1
			}
			if m.execListCursor >= m.execScroll+vis {
				m.execScroll = m.execListCursor - vis + 1
			}
		}

		return m, readNextStreamResult(msg.ch)

	case streamDoneMsg:
		m.execDone = true
		m.execResults = SortHostExecForUI(m.execResults)
		m.clampExecScroll()
		return m, nil

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
		if m.mode == "tunnel" || m.mode == "execinput" || m.mode == "cueexecinput" || m.mode == "filter" {
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
			if len(m.availableRecipes) > 0 {
				m.recipeCursor = 0
				m.ti.SetValue(m.availableRecipes[0])
			} else {
				m.ti.Reset()
			}
			m.ti.Placeholder = "path/to/recipe.cue (! = execute) — * rows only, or all w/ IP if none marked"
			m.ti.Focus()
			return m, textinput.Blink
		case "/":
			m.mode = "filter"
			m.ti.Placeholder = "filter hosts..."
			m.ti.SetValue(m.filter)
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
	case "up", "k", "ctrl+p":
		if m.mode == "cueexecinput" && len(m.availableRecipes) > 0 {
			m.recipeCursor--
			if m.recipeCursor < 0 {
				m.recipeCursor = len(m.availableRecipes) - 1
			}
			m.ti.SetValue(m.availableRecipes[m.recipeCursor])
			return m, nil
		}
	case "down", "j", "ctrl+n":
		if m.mode == "cueexecinput" && len(m.availableRecipes) > 0 {
			m.recipeCursor++
			if m.recipeCursor >= len(m.availableRecipes) {
				m.recipeCursor = 0
			}
			m.ti.SetValue(m.availableRecipes[m.recipeCursor])
			return m, nil
		}
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
		if m.mode == "filter" {
			m.mode = "table"
			m.ti.Blur()
			return m, nil
		}
		cmd := strings.TrimSpace(m.ti.Value())
		if cmd == "" {
			return m, nil
		}
		targets, note := m.parallelExecTargets()
		m.ti.Blur()
		return m, runParallelSSHStreamCmd(m.sshUser, targets, cmd, note)
	}
	var cmd tea.Cmd
	m.ti, cmd = m.ti.Update(msg)

	if m.mode == "filter" && m.filter != m.ti.Value() {
		m.filter = m.ti.Value()
		m.applyFilter()
	}

	return m, cmd
}

func (m *model) updateExecResultsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.execPopupOpen {
		return m.updateExecPopupKeys(msg)
	}

	maxCursor := len(m.execResults) - 1
	if maxCursor < 0 {
		maxCursor = 0
	}

	switch msg.String() {
	case "esc":
		m.mode = "table"
		m.execScroll = 0
		m.execListCursor = 0
		return m, nil
	case "q", "ctrl+c":
		m.lastAction = actNone
		return m, tea.Quit
	case "enter":
		if len(m.execResults) > 0 {
			m.execPopupOpen = true
			m.execPopupScroll = 0
		}
	case "up", "k":
		if m.execListCursor > 0 {
			m.execListCursor--
		}
	case "down", "j":
		if m.execListCursor < maxCursor {
			m.execListCursor++
		}
	case "pgup", "b":
		m.execListCursor -= 10
		if m.execListCursor < 0 {
			m.execListCursor = 0
		}
	case "pgdown", "f":
		m.execListCursor += 10
		if m.execListCursor > maxCursor {
			m.execListCursor = maxCursor
		}
	case "home", "g":
		m.execListCursor = 0
	case "end", "G":
		m.execListCursor = maxCursor
	}

	// Keep cursor in view
	vis := m.visibleExecLines() - 4 // Leave room for headers
	if vis < 1 {
		vis = 1
	}
	if m.execListCursor < m.execScroll {
		m.execScroll = m.execListCursor
	} else if m.execListCursor >= m.execScroll+vis {
		m.execScroll = m.execListCursor - vis + 1
	}

	return m, nil
}

func (m *model) updateExecPopupKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	lines := m.popupResultLines()
	vis := m.visibleExecLines()
	maxScroll := 0
	if len(lines) > vis {
		maxScroll = len(lines) - vis
	}

	switch msg.String() {
	case "esc", "enter": // allow enter to toggle it back off too
		m.execPopupOpen = false
		m.execPopupScroll = 0
		return m, nil
	case "q", "ctrl+c":
		m.lastAction = actNone
		return m, tea.Quit
	case "up", "k":
		if m.execPopupScroll > 0 {
			m.execPopupScroll--
		}
	case "down", "j":
		if m.execPopupScroll < maxScroll {
			m.execPopupScroll++
		}
	case "pgup", "b":
		m.execPopupScroll -= vis / 2
		if m.execPopupScroll < 0 {
			m.execPopupScroll = 0
		}
	case "pgdown", "f":
		m.execPopupScroll += vis / 2
		if m.execPopupScroll > maxScroll {
			m.execPopupScroll = maxScroll
		}
	case "home", "g":
		m.execPopupScroll = 0
	case "end", "G":
		m.execPopupScroll = maxScroll
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
	if m.execPopupOpen {
		if m.execPopupScroll > maxScroll {
			m.execPopupScroll = maxScroll
		}
		if m.execPopupScroll < 0 {
			m.execPopupScroll = 0
		}
		return
	}

	maxCursor := len(m.execResults) - 1
	if maxCursor < 0 {
		maxCursor = 0
	}
	if m.execListCursor > maxCursor {
		m.execListCursor = maxCursor
	}

	vis = m.visibleExecLines() - 4
	if vis < 1 {
		vis = 1
	}
	if m.execListCursor < m.execScroll {
		m.execScroll = m.execListCursor
	} else if m.execListCursor >= m.execScroll+vis {
		m.execScroll = m.execListCursor - vis + 1
	}
	if m.execScroll < 0 {
		m.execScroll = 0
	}
}

func (m *model) popupResultLines() []string {
	if !m.execPopupOpen || len(m.execResults) == 0 || m.execListCursor >= len(m.execResults) {
		return []string{"(no data)"}
	}
	r := m.execResults[m.execListCursor]
	var lines []string

	status := "ok"
	if !r.Success {
		status = "FAILED"
	}

	lines = append(lines, fmt.Sprintf("Host:     %s", r.Name))
	lines = append(lines, fmt.Sprintf("IP:       %s", r.IP))
	lines = append(lines, fmt.Sprintf("Provider: %s", r.Provider))
	lines = append(lines, fmt.Sprintf("Status:   %s", status))
	if r.ErrMsg != "" {
		lines = append(lines, fmt.Sprintf("Error:    %s", r.ErrMsg))
	}
	lines = append(lines, "")
	lines = append(lines, "--- Output ---")

	if strings.TrimSpace(r.Output) == "" {
		lines = append(lines, "(no output)")
	} else {
		lines = append(lines, strings.Split(r.Output, "\n")...)
	}

	return lines
}

func (m *model) visibleExecLines() int {
	h := m.winH - 7
	if h < 4 {
		h = 4
	}
	return h
}

func (m *model) execResultLines() []string {
	var lines []string
	if m.execCmdLine != "" {
		lines = append(lines, fmt.Sprintf("Command: %s", m.execCmdLine))
		lines = append(lines, "")
	}
	if strings.TrimSpace(m.execTargetNote) != "" {
		lines = append(lines, m.execTargetNote)
		lines = append(lines, "")
	}

	return lines
}

func (m *model) View() string {
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	switch m.mode {
	case "filter":
		help := helpStyle.Render("enter: search   esc: clear filter   q: quit")
		box := lipgloss.JoinVertical(
			lipgloss.Left,
			baseStyle.Render(m.tbl.View()),
			"Filter ("+fmt.Sprintf("%d/%d", len(m.visible), len(m.recs))+")",
			m.ti.View(),
		)
		return box + "\n" + help
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
		helpStr := "enter: run   esc: back   q: quit"
		if len(m.availableRecipes) > 0 {
			helpStr = "enter: run   esc: back   ↑/↓: cycle built-in recipes   q: quit"
		}
		help := helpStyle.Render(helpStr)
		_, scope := m.parallelExecTargets()
		box := lipgloss.JoinVertical(
			lipgloss.Left,
			"CUE recipe (selected hosts only):",
			helpStyle.Render(scope),
			m.ti.View(),
		)
		return baseStyle.Render(box) + "\n" + help
	case "execresults":
		return m.viewExecResults(helpStyle)
	default:
		help := helpStyle.Render("enter: ssh (k8s: exec)   t: tunnel   e: parallel cmd   r: cue recipe   /: filter   x: mark row   ^a: mark all   c: clear marks   q: quit")
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
		recipe, err := cuetry.ParseRemoteRecipe(raw, targets)
		if err != nil {
			return cueRecipeDoneMsg{title: title, body: targetNote + "\n\nparse: " + err.Error()}
		}

		if !execute {
			var buf bytes.Buffer
			runErr := RunCueRecipeSteps(&buf, recipe, recipeDir, targets, sshUser, execute, nil)
			body := targetNote + "\n\n" + buf.String()
			if runErr != nil {
				body += "\nError: " + runErr.Error()
			}
			return cueRecipeDoneMsg{title: title, body: body}
		}

		totalJobs := len(recipe.Steps) * len(targets)
		ch := make(chan HostExecResult, totalJobs)

		go func() {
			defer close(ch)
			_ = StreamCueRecipeSteps(recipe, recipeDir, targets, sshUser, nil, ch)
		}()

		return streamStartMsg{
			cmdLine:    recipePath,
			targetNote: targetNote,
			totalJobs:  totalJobs,
			ch:         ch,
			isCue:      true,
		}
	}
}

type streamStartMsg struct {
	cmdLine    string
	targetNote string
	totalJobs  int
	ch         chan HostExecResult
	isCue      bool
}

type streamResultMsg struct {
	res HostExecResult
	ch  chan HostExecResult
}

type streamDoneMsg struct{}

func runParallelSSHStreamCmd(user string, targets []hosts.Record, cmdLine, targetNote string) tea.Cmd {
	return func() tea.Msg {
		var jobs []hosts.Record
		for _, r := range targets {
			if isExecutableHost(r) {
				jobs = append(jobs, r)
			}
		}

		if len(jobs) == 0 {
			return parallelExecDoneMsg{
				results:    []HostExecResult{},
				cmdLine:    cmdLine,
				targetNote: targetNote + " — nothing to run",
			}
		}

		ch := make(chan HostExecResult, len(jobs))

		go func() {
			defer close(ch)
			cmdFunc := func(r hosts.Record) string {
				// Inject host variables even for direct UI commands
				env, err := cuetry.EffectiveEnvForRun(cuetry.RecipeStep{}, nil, nil, &r)
				if err != nil {
					return fmt.Sprintf("echo 'env err: %s'", err.Error())
				}
				remoteCmd, err := cuetry.ShellExportPrefixForRemote(env, cmdLine)
				if err != nil {
					return fmt.Sprintf("echo 'export err: %s'", err.Error())
				}
				return remoteCmd
			}
			_ = StreamSSHParallel(user, jobs, cmdFunc, 0, ch, nil)
		}()

		return streamStartMsg{
			cmdLine:    cmdLine,
			targetNote: targetNote,
			totalJobs:  len(jobs),
			ch:         ch,
		}
	}
}

func readNextStreamResult(ch chan HostExecResult) tea.Cmd {
	return func() tea.Msg {
		res, ok := <-ch
		if !ok {
			return streamDoneMsg{}
		}
		return streamResultMsg{res: res, ch: ch}
	}
}

// parallelExecTargets returns hosts to run a parallel command on. If at least
// one table row is marked (*), only marked rows that have PrimaryIP are used.
// If nothing is marked, every row with PrimaryIP is used.
func isExecutableHost(r hosts.Record) bool {
	return strings.TrimSpace(r.PrimaryIP) != "" || (r.Provider == "k8s" && r.Meta["kind"] == "pod")
}

func (m *model) parallelExecTargets() ([]hosts.Record, string) {
	if len(m.selected) == 0 {
		out := make([]hosts.Record, 0, len(m.recs))
		for _, r := range m.recs {
			if isExecutableHost(r) {
				out = append(out, r)
			}
		}
		note := fmt.Sprintf("Scope: all %d host(s) (no * marks — use x on rows, or ^a to mark all executable)", len(out))
		return out, note
	}
	out := make([]hosts.Record, 0, len(m.selected))
	skippedNoIP := 0
	for i, r := range m.recs {
		if _, ok := m.selected[i]; !ok {
			continue
		}
		if !isExecutableHost(r) {
			skippedNoIP++
			continue
		}
		out = append(out, r)
	}
	note := fmt.Sprintf("Scope: %d host(s) from %d marked row(s)", len(out), len(m.selected))
	if skippedNoIP > 0 {
		note += fmt.Sprintf(" (%d marked row(s) not executable)", skippedNoIP)
	}
	return out, note
}

func (m *model) applyFilter() {
	term := strings.TrimSpace(strings.ToLower(m.filter))
	if term == "" {
		vis := make([]int, len(m.recs))
		for i := range m.recs {
			vis[i] = i
		}
		m.visible = vis
	} else {
		var vis []int
		for i, r := range m.recs {
			if strings.Contains(strings.ToLower(r.Name), term) || strings.Contains(strings.ToLower(r.PrimaryIP), term) {
				vis = append(vis, i)
			}
		}
		m.visible = vis
	}
	m.refreshTableRows(0) // reset cursor to top
}

func (m *model) refreshTableRows(preserveCursor int) {
	m.tbl.SetRows(rowsFromRecs(m.recs, m.visible, m.selected))
	if preserveCursor >= 0 && preserveCursor < len(m.visible) {
		m.tbl.SetCursor(preserveCursor)
	}
}

func (m *model) toggleParallelMark() {
	cur := m.tbl.Cursor()
	if cur < 0 || cur >= len(m.visible) {
		return
	}
	realIdx := m.visible[cur]
	if _, ok := m.selected[realIdx]; ok {
		delete(m.selected, realIdx)
	} else {
		m.selected[realIdx] = struct{}{}
	}
	m.refreshTableRows(cur)
}

func (m *model) selectAllWithIPForParallel() {
	m.selected = make(map[int]struct{})
	for i, r := range m.recs {
		if isExecutableHost(r) {
			m.selected[i] = struct{}{}
		}
	}
	m.refreshTableRows(m.tbl.Cursor())
}

func (m *model) clearParallelMarks() {
	m.selected = make(map[int]struct{})
	m.refreshTableRows(m.tbl.Cursor())
}

func runSSH(user string, r hosts.Record) error {
	if r.PrimaryIP == "" && (r.Provider != "k8s" || r.Meta["kind"] != "pod") {
		return fmt.Errorf("no IP for selected host")
	}
	executor := GetExecutor(r)
	return executor.RunInteractive(user, r)
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
