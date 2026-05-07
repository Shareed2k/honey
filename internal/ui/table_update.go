package ui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

func (m *model) handleStreamStartMsg(msg streamStartMsg) (tea.Model, tea.Cmd) {
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
}

func (m *model) handleStreamResultMsg(msg streamResultMsg) (tea.Model, tea.Cmd) {
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
}

func (m *model) handleStreamDoneMsg(_ streamDoneMsg) (tea.Model, tea.Cmd) {
	m.execDone = true
	m.execResults = SortHostExecForUI(m.execResults)
	m.clampExecScroll()
	return m, nil
}

func (m *model) handleParallelExecDoneMsg(msg parallelExecDoneMsg) (tea.Model, tea.Cmd) {
	m.cueResultBody = ""
	m.cueResultTitle = "Parallel SSH results"
	m.execResults = SortHostExecForUI(msg.results)
	m.execCmdLine = msg.cmdLine
	m.execTargetNote = msg.targetNote
	m.execScroll = 0
	m.mode = "execresults"
	m.ti.Blur()
	return m, nil
}

func (m *model) handleCueRecipeDoneMsg(msg cueRecipeDoneMsg) (tea.Model, tea.Cmd) {
	m.cueResultTitle = msg.title
	m.cueResultBody = msg.body
	m.execResults = nil
	m.execCmdLine = ""
	m.execTargetNote = ""
	m.execScroll = 0
	m.mode = "execresults"
	m.ti.Blur()
	return m, nil
}

func (m *model) handleWindowSizeMsg(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.winW = msg.Width
	m.winH = msg.Height

	m.tbl.SetWidth(msg.Width - 4)
	m.tbl.SetHeight(msg.Height - 8)
	m.tbl.SetColumns(recalculateTableColumns(msg.Width - 4))

	m.ti.SetWidth(msg.Width - 8)
	m.clampExecScroll()
	return m, nil
}

func (m *model) handleTableKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	case "R":
		if m.recordDir != "" {
			m.recordEnabled = !m.recordEnabled
		}
		return m, nil
	case "enter":
		m.lastAction = actSSH
		return m, tea.Quit
	case "t":
		m.mode = "tunnel"
		m.tunnelLocalPort.Reset()
		m.tunnelRemoteHost.SetValue("localhost")
		m.tunnelRemotePort.Reset()
		m.tunnelFocusIndex = 0
		m.tunnelLocalPort.Focus()
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
		}
		m.ti.Reset()
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
