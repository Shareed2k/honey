package ui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/pvelxc"
	"github.com/shareed2k/honey/internal/safepath"
	k8sexec "k8s.io/client-go/util/exec"
)

type action int

const (
	actNone action = iota
	actSSH
	actTunnel
	actReplay
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

// RunTableOptions configures optional session recording for the TUI table.
type RunTableOptions struct {
	RecordDir     string
	RecordEnabled bool
	// Config is the honey YAML already loaded for this session (optional; nil if none / load failed).
	Config *config.File
	// ConfigPath is the resolved honey YAML path (may be empty); CUE agent_transfer steps with cloud_backend_ref need it.
	ConfigPath string
}

type model struct {
	recs    []hosts.Record
	tbl     table.Model
	ti      textinput.Model
	sshUser string
	mode    string // table | tunnel | execinput | execresults | filter | replaypick | agenttransferform
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
	cuePreviewOpen   bool
	cuePreviewScroll int

	// Tunnel popup state
	tunnelLocalPort  textinput.Model
	tunnelRemoteHost textinput.Model
	tunnelRemotePort textinput.Model
	tunnelFocusIndex int

	recordDir     string
	recordEnabled bool
	honey         *config.File
	configPath    string
	// batchRecorder is non-nil while streaming parallel exec or CUE execute results when recording is on.
	batchRecorder *SessionRecorder

	// replaypick: choose a .hrec.jsonl under recordDir; on enter, lastAction=actReplay + replayFileName.
	replayFiles      []string
	replayListErr    string
	replayFileName   string
	replayCursor     int
	replayPickScroll int

	// fileClientCache is shared by agent transfer (key a) and other pooled SSH clients.
	fileClientCache *ClientCache

	// A → cloud → B agent transfer wizard (key a).
	agentPick       string // "source" | "dest" | ""
	agentSrc        hosts.Record
	agentDst        hosts.Record
	agentFormStep   int
	agentFormValues [9]string
	agentStorageIdx int // 0 = s3, 1 = googlecloudstorage (step 2 picker)
	agentAwaitKeep  bool
	agentKeepObject bool
}

var baseStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.NormalBorder()).
	BorderForeground(lipgloss.Color("240"))

// RunTable shows an interactive table and optionally execs ssh.
// After SSH/Tunnel disconnects, it returns to the UI.
func RunTable(records []hosts.Record, sshUser string, opts RunTableOptions) error {
	if len(records) == 0 {
		fmt.Fprintln(os.Stderr, "no matching hosts")
		return nil
	}

	m := newModel(records, sshUser, opts)
	defer m.fileClientCache.CloseAll()

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

		if lastAction == actReplay {
			if name := strings.TrimSpace(m.replayFileName); name != "" {
				_ = RunRecordingReplay(m.recordDir, name)
			}
			m.replayFileName = ""
			continue
		}

		row := fm.tbl.Cursor()
		if row < 0 || row >= len(fm.visible) {
			return nil
		}
		realIdx := fm.visible[row]
		r := fm.recs[realIdx]

		switch lastAction {
		case actSSH:
			err = runSSHWithRecording(fm.sshUser, r, fm.recordingOptions("tui", "interactive"))
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

		var labels []string
		for k, v := range r.Meta {
			if strings.HasPrefix(k, "label_") {
				labels = append(labels, fmt.Sprintf("%s=%s", strings.TrimPrefix(k, "label_"), v))
			}
		}

		// Sort labels manually
		for i := 0; i < len(labels)-1; i++ {
			for j := i + 1; j < len(labels); j++ {
				if labels[i] > labels[j] {
					labels[i], labels[j] = labels[j], labels[i]
				}
			}
		}

		if tagsStr, ok := r.Meta["tags"]; ok && tagsStr != "" {
			tags := strings.Split(tagsStr, ",")
			for i := range tags {
				tags[i] = strings.TrimSpace(tags[i])
			}
			// Sort tags manually
			for i := 0; i < len(tags)-1; i++ {
				for j := i + 1; j < len(tags); j++ {
					if tags[i] > tags[j] {
						tags[i], tags[j] = tags[j], tags[i]
					}
				}
			}
			if len(labels) > 0 {
				labels = append(tags, labels...)
			} else {
				labels = tags
			}
		}

		labelStr := strings.Join(labels, ", ")

		rows = append(rows, table.Row{mark, r.Provider, r.Name, r.PrimaryIP, r.Zone, reg, labelStr})
	}
	return rows
}

func newModel(records []hosts.Record, sshUser string, opts RunTableOptions) *model {
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
		recordDir:        strings.TrimSpace(opts.RecordDir),
		recordEnabled:    strings.TrimSpace(opts.RecordDir) != "" && opts.RecordEnabled,
		honey:            opts.Config,
		configPath:       strings.TrimSpace(opts.ConfigPath),
		fileClientCache:  NewClientCache(),
	}
}

func (m *model) Init() tea.Cmd {
	return nil
}

// pasteMsgUpdatesTextInput reports whether bracketed paste should update the text field (not the table).
func (m *model) pasteMsgUpdatesTextInput() bool {
	return m.mode == "execinput" || m.mode == "cueexecinput" ||
		(m.mode == "agenttransferform" && !m.agentAwaitKeep && m.agentFormStep != 2)
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
	case agentTransferDoneMsg:
		return m.handleAgentTransferDoneMsg(msg)
	case tea.PasteMsg:
		// On macOS terminals, Cmd+V often arrives as bracketed paste, not KeyMsg.
		if m.pasteMsgUpdatesTextInput() {
			m.applyPastedText(msg.Content)
			return m, nil
		}
	case tea.WindowSizeMsg:
		return m.handleWindowSizeMsg(msg)
	case tea.KeyMsg:
		return m.dispatchKeyMsg(msg)
	}

	var cmd tea.Cmd
	m.tbl, cmd = m.tbl.Update(msg)
	return m, cmd
}

func (m *model) updateTextInputMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == "cueexecinput" && m.cuePreviewOpen {
		return m.updateCuePreviewKeys(msg)
	}

	if model, ok := m.tryClipboardPasteInTextInput(msg); ok {
		return model, nil
	}
	if model, ok := m.tryCueRecipeListKeys(msg); ok {
		return model, nil
	}

	switch msg.String() {
	case "esc":
		m.mode = "table"
		m.ti.Blur()
		return m, nil
	case "enter":
		return m.textInputEnter()
	default:
		var cmd tea.Cmd
		m.ti, cmd = m.ti.Update(msg)

		if m.mode == "filter" && m.filter != m.ti.Value() {
			m.filter = m.ti.Value()
			m.applyFilter()
		}

		return m, cmd
	}
}

func (m *model) tryClipboardPasteInTextInput(msg tea.KeyMsg) (tea.Model, bool) {
	switch msg.String() {
	case "ctrl+v", "shift+insert", "cmd+v", "meta+v", "super+v":
		if m.pasteMsgUpdatesTextInput() {
			if clip, err := clipboard.ReadAll(); err == nil {
				m.applyPastedText(clip)
			}
			return m, true
		}
	}
	return m, false
}

func (m *model) tryCueRecipeListKeys(msg tea.KeyMsg) (tea.Model, bool) {
	if m.mode != "cueexecinput" || len(m.availableRecipes) == 0 {
		return m, false
	}
	switch msg.String() {
	case "tab":
		m.selectCurrentDefaultRecipe()
		return m, true
	case "v":
		m.cuePreviewOpen = !m.cuePreviewOpen
		if !m.cuePreviewOpen {
			m.cuePreviewScroll = 0
		}
		return m, true
	case "up", "k", "ctrl+p":
		m.recipeCursor--
		if m.recipeCursor < 0 {
			m.recipeCursor = len(m.availableRecipes) - 1
		}
		return m, true
	case "down", "j", "ctrl+n":
		m.recipeCursor++
		if m.recipeCursor >= len(m.availableRecipes) {
			m.recipeCursor = 0
		}
		return m, true
	default:
		return m, false
	}
}

func (m *model) textInputEnter() (tea.Model, tea.Cmd) {
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
		return m, runCueRecipeCmd(val, targets, note, m.sshUser, execute, m.recordDir, m.recordEnabled, m.honey, m.configPath)
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

func (m *model) applyPastedText(raw string) {
	paste := normalizePastedInput(m.mode, raw)
	if paste == "" {
		return
	}
	cur := m.ti.Value()
	if cur == "" {
		m.ti.SetValue(paste)
		return
	}
	sep := ""
	if !strings.HasSuffix(cur, " ") && !strings.HasPrefix(paste, " ") {
		sep = " "
	}
	m.ti.SetValue(cur + sep + paste)
}

func (m *model) selectCurrentDefaultRecipe() {
	if len(m.availableRecipes) == 0 {
		return
	}
	if m.recipeCursor < 0 || m.recipeCursor >= len(m.availableRecipes) {
		m.recipeCursor = 0
	}
	m.ti.SetValue(m.availableRecipes[m.recipeCursor])
}

func (m *model) updateCuePreviewKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	lines := m.selectedDefaultRecipePreviewLines(0)
	vis := m.visibleCuePreviewLines()
	maxScroll := 0
	if len(lines) > vis {
		maxScroll = len(lines) - vis
	}

	switch msg.String() {
	case "esc", "enter", "v":
		m.cuePreviewOpen = false
		m.cuePreviewScroll = 0
	case "up", "k":
		if m.cuePreviewScroll > 0 {
			m.cuePreviewScroll--
		}
	case "down", "j":
		if m.cuePreviewScroll < maxScroll {
			m.cuePreviewScroll++
		}
	case "pgup", "b":
		m.cuePreviewScroll -= vis / 2
		if m.cuePreviewScroll < 0 {
			m.cuePreviewScroll = 0
		}
	case "pgdown", "f":
		m.cuePreviewScroll += vis / 2
		if m.cuePreviewScroll > maxScroll {
			m.cuePreviewScroll = maxScroll
		}
	case "home", "g":
		m.cuePreviewScroll = 0
	case "end", "G":
		m.cuePreviewScroll = maxScroll
	}

	return m, nil
}

func normalizePastedInput(mode, raw string) string {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if mode == "cueexecinput" {
		return strings.TrimSpace(strings.Split(s, "\n")[0])
	}
	return strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
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

	if strings.TrimSpace(r.HookPhase) != "" || strings.TrimSpace(r.HookOutput) != "" {
		lines = append(lines, "")
		lines = append(lines, "--- Hook ("+strings.TrimSpace(r.HookPhase)+") ---")
		if strings.TrimSpace(r.HookOutput) == "" {
			lines = append(lines, "(no hook output)")
		} else {
			lines = append(lines, strings.Split(strings.TrimSpace(r.HookOutput), "\n")...)
		}
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
		help := helpStyle.Render(fmt.Sprintf("enter: run   %s: paste   esc: back   q: quit", pasteShortcutHelp()))
		_, scope := m.parallelExecTargets()
		box = lipgloss.JoinVertical(
			lipgloss.Left,
			"Parallel SSH:",
			helpStyle.Render(scope),
			m.ti.View(),
		)
		box = baseStyle.Render(box) + "\n" + help
	case "cueexecinput":
		var helpStr string
		if len(m.availableRecipes) > 0 {
			helpStr = fmt.Sprintf("enter: run   %s: paste   tab: use selected default   v: preview popup   esc: back   ↑/↓: move defaults   q: quit", pasteShortcutHelp())
		} else {
			helpStr = fmt.Sprintf("enter: run   %s: paste   esc: back   q: quit", pasteShortcutHelp())
		}
		help := helpStyle.Render(helpStr)
		_, scope := m.parallelExecTargets()
		left := lipgloss.JoinVertical(
			lipgloss.Left,
			"CUE recipe (selected hosts only):",
			helpStyle.Render(scope),
			m.ti.View(),
		)
		if len(m.availableRecipes) > 0 {
			right := m.viewDefaultRecipesPanel()
			box = lipgloss.JoinVertical(
				lipgloss.Left,
				left,
				"",
				right,
			)
		} else {
			box = left
		}
		box = baseStyle.Render(box) + "\n" + help
	case "agenttransferform":
		box = baseStyle.Render(m.viewAgentTransferForm(helpStyle))
	case "execresults":
		box = m.viewExecResults(helpStyle)
	case "replaypick":
		box = m.viewReplayPick(helpStyle)
	default:
		recHint := ""
		if m.recordDir != "" {
			recState := "off"
			if m.recordEnabled {
				recState = "on"
			}
			recHint = "   R: record " + recState + "   p: play recording"
		}
		help := helpStyle.Render("enter: ssh (k8s: exec)   a: A→cloud→B   t: tunnel   e: parallel cmd   r: cue recipe   /: filter   x: mark row   ^a: mark all   c: clear marks" + recHint + "   q: quit")
		nMark := len(m.selected)
		sub := ""
		if nMark > 0 {
			sub = helpStyle.Render(fmt.Sprintf("%d row(s) marked (* for parallel SSH and CUE recipe)", nMark)) + "\n"
		}
		title := lipgloss.NewStyle().Bold(true).Render("honey — select a host")
		banner := ""
		switch m.agentPick {
		case "source":
			banner = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("Pick SOURCE host for agent transfer — Enter on row   Esc cancel") + "\n\n"
		case "dest":
			banner = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("Pick DESTINATION host — Enter on row   Esc cancel") + "\n\n"
		}
		box = title + "\n" + banner + sub + baseStyle.Render(m.tbl.View()) + "\n" + help
	}

	view := tea.NewView(box)
	if m.mode == "cueexecinput" && m.cuePreviewOpen {
		view = tea.NewView(m.viewCueRecipePreviewPopup(helpStyle))
	}
	view.AltScreen = true
	return view
}

func pasteShortcutHelp() string {
	if runtime.GOOS == "darwin" {
		return "cmd+v"
	}
	return "ctrl+v/shift+insert"
}

func (m *model) viewDefaultRecipesPanel() string {
	rows := make([]string, 0, 3+len(m.availableRecipes))
	rows = append(rows, lipgloss.NewStyle().Bold(true).Render("Default recipes"))
	rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("↑/↓ move, tab inserts, v opens popup"))
	rows = append(rows, "")

	for i, recipe := range m.availableRecipes {
		displayName := filepath.Base(recipe)
		if displayName == "." || displayName == string(filepath.Separator) || displayName == "" {
			displayName = recipe
		}
		prefix := "  "
		if i == m.recipeCursor {
			prefix = "> "
			displayName = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229")).Render(displayName)
		}
		rows = append(rows, prefix+displayName)
	}
	return strings.Join(rows, "\n")
}

func (m *model) replayPickVisibleLines() int {
	h := m.winH - 10
	if h < 4 {
		h = 4
	}
	return h
}

func (m *model) clampReplayPickScroll() {
	if len(m.replayFiles) == 0 {
		m.replayPickScroll = 0
		return
	}
	vis := m.replayPickVisibleLines()
	maxScroll := len(m.replayFiles) - vis
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.replayPickScroll > maxScroll {
		m.replayPickScroll = maxScroll
	}
	if m.replayPickScroll < 0 {
		m.replayPickScroll = 0
	}
	if m.replayCursor < m.replayPickScroll {
		m.replayPickScroll = m.replayCursor
	}
	if vis > 0 && m.replayCursor >= m.replayPickScroll+vis {
		m.replayPickScroll = m.replayCursor - vis + 1
	}
}

func (m *model) viewReplayPick(helpStyle lipgloss.Style) string {
	title := lipgloss.NewStyle().Bold(true).Render("Session recordings")
	var lines []string
	if m.replayListErr != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render(m.replayListErr))
	}
	if len(m.replayFiles) == 0 && m.replayListErr == "" {
		lines = append(lines, helpStyle.Render("(no .hrec.jsonl files in record dir)"))
	} else if len(m.replayFiles) > 0 {
		vis := m.replayPickVisibleLines()
		start := m.replayPickScroll
		end := start + vis
		if end > len(m.replayFiles) {
			end = len(m.replayFiles)
		}
		for i := start; i < end; i++ {
			name := m.replayFiles[i]
			prefix := "  "
			line := name
			if i == m.replayCursor {
				prefix = "> "
				line = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229")).Render(name)
			}
			lines = append(lines, prefix+line)
		}
		if len(m.replayFiles) > vis {
			lines = append(lines, helpStyle.Render(fmt.Sprintf("(rows %d–%d of %d — pgup/pgdn)", start+1, end, len(m.replayFiles))))
		}
	}
	body := strings.Join(lines, "\n")
	help := helpStyle.Render("enter: play   esc: back   ↑/↓: move   pgup/pgdn: page   q: back")
	return lipgloss.JoinVertical(lipgloss.Left, title, "", body, "", help)
}

func (m *model) selectedDefaultRecipePath() string {
	if len(m.availableRecipes) == 0 {
		return ""
	}
	if m.recipeCursor < 0 || m.recipeCursor >= len(m.availableRecipes) {
		return m.availableRecipes[0]
	}
	return m.availableRecipes[m.recipeCursor]
}

func (m *model) selectedDefaultRecipePreviewLines(maxLines int) []string {
	recipePath := m.selectedDefaultRecipePath()
	if recipePath == "" {
		return []string{"(no recipe selected)"}
	}

	raw, err := safepath.ReadFile(recipePath)
	if err != nil {
		absPath, absErr := filepath.Abs(recipePath)
		if absErr == nil {
			raw, err = safepath.ReadFile(absPath)
		}
	}
	if err != nil {
		return []string{fmt.Sprintf("(failed to read recipe: %v)", err)}
	}

	text := strings.TrimRight(string(raw), "\n")
	if strings.TrimSpace(text) == "" {
		return []string{"(empty recipe file)"}
	}

	lines := strings.Split(text, "\n")
	if maxLines > 0 && len(lines) > maxLines {
		lines = append(lines[:maxLines], fmt.Sprintf("... (%d more lines)", len(lines)-maxLines))
	}
	return lines
}

func (m *model) visibleCuePreviewLines() int {
	vis := m.winH - 12
	if vis < 8 {
		vis = 8
	}
	return vis
}

func (m *model) viewCueRecipePreviewPopup(helpStyle lipgloss.Style) string {
	lines := m.selectedDefaultRecipePreviewLines(0)
	vis := m.visibleCuePreviewLines()
	start := m.cuePreviewScroll
	if start < 0 {
		start = 0
	}
	if start > len(lines) {
		start = len(lines)
	}
	end := start + vis
	if end > len(lines) {
		end = len(lines)
	}

	scrollNote := ""
	if len(lines) > vis && end > 0 {
		scrollNote = fmt.Sprintf("lines %d-%d of %d", start+1, end, len(lines))
	}

	previewBody := "(empty preview)"
	if end > start {
		previewBody = strings.Join(highlightCueLines(lines[start:end]), "\n")
	}

	recipeName := filepath.Base(m.selectedDefaultRecipePath())
	title := "Recipe Preview"
	if recipeName != "" {
		title = "Recipe Preview: " + recipeName
	}
	popupWidth := m.winW - 8
	if popupWidth < 50 {
		popupWidth = 50
	}
	popup := lipgloss.JoinVertical(
		lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Render(title),
		baseStyle.Width(popupWidth).Render(previewBody),
		helpStyle.Render(scrollNote),
		helpStyle.Render("esc/v/enter: close   ↑/k ↓/j   pgup/pgdn   home/end"),
	)
	return lipgloss.Place(m.winW, m.winH, lipgloss.Center, lipgloss.Center, popup)
}

func highlightCueLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, highlightCueLine(line))
	}
	return out
}

func highlightCueLine(line string) string {
	commentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	stringStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("221"))
	keywordStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)

	commentIdx := cueLineCommentIndex(line)
	codePart := line
	commentPart := ""
	if commentIdx >= 0 {
		codePart = line[:commentIdx]
		commentPart = line[commentIdx:]
	}

	var b strings.Builder
	for i := 0; i < len(codePart); {
		ch := codePart[i]
		if ch == '"' {
			j := i + 1
			escaped := false
			for j < len(codePart) {
				if escaped {
					escaped = false
					j++
					continue
				}
				if codePart[j] == '\\' {
					escaped = true
					j++
					continue
				}
				if codePart[j] == '"' {
					j++
					break
				}
				j++
			}
			b.WriteString(stringStyle.Render(codePart[i:j]))
			i = j
			continue
		}
		if isCueWordStart(ch) {
			j := i + 1
			for j < len(codePart) && isCueWordChar(codePart[j]) {
				j++
			}
			word := codePart[i:j]
			if isCueKeyword(word) {
				b.WriteString(keywordStyle.Render(word))
			} else {
				b.WriteString(word)
			}
			i = j
			continue
		}
		b.WriteByte(ch)
		i++
	}

	if commentPart != "" {
		b.WriteString(commentStyle.Render(commentPart))
	}

	return b.String()
}

func cueLineCommentIndex(line string) int {
	inString := false
	escaped := false
	for i := 0; i < len(line)-1; i++ {
		ch := line[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			continue
		}
		if ch == '/' && line[i+1] == '/' {
			return i
		}
	}
	return -1
}

func isCueWordStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isCueWordChar(ch byte) bool {
	return isCueWordStart(ch) || (ch >= '0' && ch <= '9')
}

func isCueKeyword(word string) bool {
	switch word {
	case "package", "import", "let", "for", "if", "in", "true", "false", "null":
		return true
	default:
		return false
	}
}

func runCueRecipeCmd(recipePath string, targets []hosts.Record, targetNote string, sshUser string, execute bool, recordDir string, recordEnabled bool, honey *config.File, configPath string) tea.Cmd {
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
		pluginMgr, perr := plugins.Open(context.Background(), honey)
		if perr != nil {
			return cueRecipeDoneMsg{title: title, body: targetNote + "\n\nplugins: " + perr.Error()}
		}
		defer func() { _ = pluginMgr.Close() }()
		recipe, err := cuetry.ParseRemoteRecipeOpts(raw, targets, cuetry.ParseOptions{PluginManager: pluginMgr})
		if err != nil {
			return cueRecipeDoneMsg{title: title, body: targetNote + "\n\nparse: " + err.Error()}
		}

		if !execute {
			var buf bytes.Buffer
			aiPrompt := LoadAISystemPromptFromConfigPath(configPath)
			secRes, err := cuetry.NewSecretResolverWithPlugins(cuetry.SecretResolverOptionsFromHoney(honey), pluginMgr)
			if err != nil {
				return cueRecipeDoneMsg{title: title, body: targetNote + "\n\nsecrets: " + err.Error()}
			}
			runErr := RunCueRecipeSteps(context.Background(), &buf, recipe, recipeDir, targets, sshUser, execute, nil, configPath, aiPrompt, secRes, pluginMgr, nil)
			if recordEnabled && strings.TrimSpace(recordDir) != "" && len(targets) > 0 {
				if rec, err := NewBatchSessionRecorder(recordDir, "tui-cue-exec-dry", sshUser, len(targets)); err == nil {
					if rec != nil {
						hash, _ := cuetry.HashRecipeJSON(recipe)
						rec.RecordRecipeMeta(RecipeMeta{
							RecipePath:        absRecipe,
							HostCount:         len(targets),
							RecipeContentHash: hash,
							StartedAt:         time.Now().UTC(),
						})
					}
					if runErr != nil {
						rec.RecordError(runErr)
					} else {
						plan := buf.String()
						if strings.TrimSpace(plan) == "" {
							rec.RecordData("plan", []byte("(empty plan)"))
						} else {
							rec.RecordData("plan", []byte(plan))
						}
					}
					_ = rec.Close()
				}
			}
			body := targetNote + "\n\n" + buf.String()
			if runErr != nil {
				body += "\nError: " + runErr.Error()
			}
			return cueRecipeDoneMsg{title: title, body: body}
		}

		totalJobs, cntErr := cuetry.CountRecipeStreamResults(recipe, targets)
		if cntErr != nil {
			return cueRecipeDoneMsg{title: title, body: targetNote + "\n\nrecipe steps: " + cntErr.Error()}
		}
		if totalJobs < 1 {
			totalJobs = 1
		}
		ch := make(chan HostExecResult, totalJobs)

		go func() {
			defer close(ch)
			aiPrompt := LoadAISystemPromptFromConfigPath(configPath)
			secRes, err := cuetry.NewSecretResolverWithPlugins(cuetry.SecretResolverOptionsFromHoney(honey), pluginMgr)
			if err != nil {
				ch <- HostExecResult{Name: "cue recipe", Success: false, ErrMsg: "secrets: " + err.Error()}
				return
			}
			_ = StreamCueRecipeSteps(context.Background(), recipe, recipeDir, targets, sshUser, nil, configPath, aiPrompt, secRes, pluginMgr, true, ch)
		}()

		return streamStartMsg{
			cmdLine:    recipePath,
			targetNote: targetNote,
			totalJobs:  totalJobs,
			ch:         ch,
			isCue:      true,
			recipe:     &recipe,
			recipePath: absRecipe,
		}
	}
}

type streamStartMsg struct {
	cmdLine    string
	targetNote string
	totalJobs  int
	ch         chan HostExecResult
	isCue      bool
	// For cue-exec only: parsed recipe and absolute recipe path so the
	// session recorder can attribute the recording to a recipe.
	recipe     *cuetry.Recipe
	recipePath string
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
			cmdFunc := func(r hosts.Record, _ map[string]string) string {
				// Inject host variables even for direct UI commands
				env, err := cuetry.EffectiveEnvHostOnly(&r)
				if err != nil {
					return fmt.Sprintf("echo 'env err: %s'", err.Error())
				}
				remoteCmd, err := cuetry.ShellExportPrefixForRemote(env, cmdLine)
				if err != nil {
					return fmt.Sprintf("echo 'export err: %s'", err.Error())
				}
				return remoteCmd
			}
			_ = StreamSSHParallel(context.Background(), user, jobs, false, cmdFunc, 0, ch, nil, nil, false, nil)
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

func (m *model) recordingOptions(trigger, mode string) *SessionRecorderOptions {
	if !m.recordEnabled || strings.TrimSpace(m.recordDir) == "" {
		return nil
	}
	row := m.tbl.Cursor()
	if row < 0 || row >= len(m.visible) {
		return nil
	}
	realIdx := m.visible[row]
	r := m.recs[realIdx]
	return &SessionRecorderOptions{
		Dir:      m.recordDir,
		Trigger:  trigger,
		Mode:     mode,
		Provider: r.Provider,
		HostName: r.Name,
		HostIP:   r.PrimaryIP,
		User:     m.sshUser,
	}
}

func runSSHWithRecording(user string, r hosts.Record, recordOpts *SessionRecorderOptions) error {
	if r.PrimaryIP == "" && (r.Provider != "k8s" || r.Meta["kind"] != "pod") && !pvelxc.ShouldUsePVETTY(r) {
		return fmt.Errorf("no IP for selected host")
	}
	var recorder *SessionRecorder
	if recordOpts != nil {
		rec, err := NewSessionRecorder(*recordOpts)
		if err == nil {
			recorder = rec
		}
	}
	if recorder != nil {
		defer recorder.Close()
	}
	if r.Provider == "k8s" && r.Meta["kind"] == "pod" {
		return runK8sInteractiveWithRecorder(user, r, recorder)
	}
	return runSSHInteractive(user, r, recorder)
}

func runTunnel(user string, r hosts.Record, localFwd string) error {
	if r.PrimaryIP == "" && (r.Provider != "k8s" || r.Meta["kind"] != "pod") {
		return fmt.Errorf("no IP for selected host")
	}
	if localFwd == "" || !strings.Contains(localFwd, ":") {
		return fmt.Errorf("tunnel spec must look like 8080:remotehost:8080 or 8080:8080 for kubernetes pods")
	}
	executor := GetExecutor(r)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	return executor.RunTunnel(ctx, user, r, localFwd, os.Stderr)
}
