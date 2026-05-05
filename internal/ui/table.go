package ui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/safepath"
	k8sexec "k8s.io/client-go/util/exec"
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

	// Tunnel popup state
	tunnelLocalPort  textinput.Model
	tunnelRemoteHost textinput.Model
	tunnelRemotePort textinput.Model
	tunnelFocusIndex int
}

var baseStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.NormalBorder()).
	BorderForeground(lipgloss.Color("240"))

// RunTable shows an interactive table and optionally execs ssh.
// After SSH/Tunnel disconnects, it returns to the UI.
func RunTable(records []hosts.Record, sshUser string) error {
	if len(records) == 0 {
		fmt.Fprintln(os.Stderr, "no matching hosts")
		return nil
	}

	m := newModel(records, sshUser)

	for {
		p := tea.NewProgram(m)
		final, err := p.Run()
		if err != nil {
			return err
		}

		fm, ok := final.(*model)
		if !ok || fm == nil {
			return nil
		}

		lastAction := fm.lastAction // capture action before we reset it

		// Save the model state for the next loop iteration (preserves cursor, marks, inputs)
		m = fm
		m.lastAction = actNone // Reset action for the next run so 'q' gracefully exits

		row := fm.tbl.Cursor()
		if row < 0 || row >= len(fm.visible) {
			return nil
		}
		realIdx := fm.visible[row]
		r := fm.recs[realIdx]

		switch lastAction {
		case actSSH:
			err = runSSH(fm.sshUser, r)
			if err != nil {
				// Avoid "ExitError" halting the TUI when users just type 'exit 1' or Ctrl+C
				_, isSSHExitErr := err.(*ssh.ExitError)

				// Kubernetes exec returns an unwrapped error matching "command terminated with exit code"
				// but let's check both the formal interface and the string to be safe.
				_, isK8sExitErr := err.(k8sexec.ExitError)
				isK8sStringErr := strings.Contains(err.Error(), "command terminated with exit code")

				if !isSSHExitErr && !isK8sExitErr && !isK8sStringErr {
					fmt.Fprintf(os.Stderr, "\r\n[honey] SSH Connection Error: %v\r\n", err)
					fmt.Fprintf(os.Stderr, "[honey] Press ENTER to return to the host list...")
					var b [1]byte
					_, _ = os.Stdin.Read(b[:])
				}
			}
			continue
		case actTunnel:
			err = runTunnel(fm.sshUser, r, fm.tunnelArg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\r\n[honey] Tunnel Connection Error: %v\r\n", err)
				fmt.Fprintf(os.Stderr, "[honey] Press ENTER to return to the host list...")
				var b [1]byte
				_, _ = os.Stdin.Read(b[:])
			}
			continue
		default:
			// "q", "ctrl+c" or esc to actually quit
			return nil
		}
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

	rows := rowsFromRecs(records, vis, sel)
	t := table.New(
		table.WithColumns(recalculateTableColumns(100)), // Fallback initial width
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
	ti.SetWidth(60)

	tunLocal := textinput.New()
	tunLocal.Placeholder = "8080"
	tunLocal.Prompt = ""
	tunLocal.CharLimit = 5
	tunLocal.SetWidth(10)

	tunHost := textinput.New()
	tunHost.Placeholder = "localhost"
	tunHost.SetValue("localhost")
	tunHost.Prompt = ""
	tunHost.CharLimit = 100
	tunHost.SetWidth(20)

	tunRemote := textinput.New()
	tunRemote.Placeholder = "8080"
	tunRemote.Prompt = ""
	tunRemote.CharLimit = 5
	tunRemote.SetWidth(10)

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
		tunnelLocalPort:  tunLocal,
		tunnelRemoteHost: tunHost,
		tunnelRemotePort: tunRemote,
		tunnelFocusIndex: 0,
	}
}

func (m *model) Init() tea.Cmd {
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case streamStartMsg:
		return m.handleStreamStartMsg(msg)
	case streamResultMsg:
		return m.handleStreamResultMsg(msg)
	case streamDoneMsg:
		return m.handleStreamDoneMsg(msg)
	case parallelExecDoneMsg:
		return m.handleParallelExecDoneMsg(msg)
	case cueRecipeDoneMsg:
		return m.handleCueRecipeDoneMsg(msg)
	case tea.WindowSizeMsg:
		return m.handleWindowSizeMsg(msg)
	case tea.KeyMsg:
		if m.mode == "execresults" {
			return m.updateExecResultsKeys(msg)
		}
		if m.mode == "tunnel" {
			return m.updateTunnelInputs(msg)
		}
		if m.mode == "execinput" || m.mode == "cueexecinput" || m.mode == "filter" {
			return m.updateTextInputMode(msg)
		}
		return m.handleTableKeyMsg(msg)
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

func (m *model) View() tea.View {
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	var box string

	switch m.mode {
	case "filter":
		help := helpStyle.Render("enter: search   esc: clear filter   q: quit")
		box = lipgloss.JoinVertical(
			lipgloss.Left,
			baseStyle.Render(m.tbl.View()),
			"Filter ("+fmt.Sprintf("%d/%d", len(m.visible), len(m.recs))+")",
			m.ti.View(),
		)
		box += "\n" + help
	case "tunnel":
		box = m.viewTunnel(helpStyle)
	case "execinput":
		help := helpStyle.Render("enter: run   esc: back   q: quit")
		_, scope := m.parallelExecTargets()
		box = lipgloss.JoinVertical(
			lipgloss.Left,
			"Parallel SSH:",
			helpStyle.Render(scope),
			m.ti.View(),
		)
		box = baseStyle.Render(box) + "\n" + help
	case "cueexecinput":
		helpStr := "enter: run   esc: back   q: quit"
		if len(m.availableRecipes) > 0 {
			helpStr = "enter: run   esc: back   ↑/↓: cycle built-in recipes   q: quit"
		}
		help := helpStyle.Render(helpStr)
		_, scope := m.parallelExecTargets()
		box = lipgloss.JoinVertical(
			lipgloss.Left,
			"CUE recipe (selected hosts only):",
			helpStyle.Render(scope),
			m.ti.View(),
		)
		box = baseStyle.Render(box) + "\n" + help
	case "execresults":
		box = m.viewExecResults(helpStyle)
	default:
		help := helpStyle.Render("enter: ssh (k8s: exec)   t: tunnel   e: parallel cmd   r: cue recipe   /: filter   x: mark row   ^a: mark all   c: clear marks   q: quit")
		nMark := len(m.selected)
		sub := ""
		if nMark > 0 {
			sub = helpStyle.Render(fmt.Sprintf("%d row(s) marked (* for parallel SSH and CUE recipe)", nMark)) + "\n"
		}
		title := lipgloss.NewStyle().Bold(true).Render("honey — select a host")
		box = title + "\n" + sub + baseStyle.Render(m.tbl.View()) + "\n" + help
	}

	view := tea.NewView(box)
	view.AltScreen = true
	return view
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

func runTunnel(user string, r hosts.Record, localFwd string) error {
	if r.PrimaryIP == "" && (r.Provider != "k8s" || r.Meta["kind"] != "pod") {
		return fmt.Errorf("no IP for selected host")
	}
	if localFwd == "" || !strings.Contains(localFwd, ":") {
		return fmt.Errorf("tunnel spec must look like 8080:remotehost:8080 or 8080:8080 for kubernetes pods")
	}
	executor := GetExecutor(r)
	return executor.RunTunnel(user, r, localFwd)
}
