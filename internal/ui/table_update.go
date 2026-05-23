package ui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/recordings"
)

func (m *model) handleStreamStartMsg(msg streamStartMsg) (tea.Model, tea.Cmd) {
	if m.batchRecorder != nil {
		_ = m.batchRecorder.Close()
		m.batchRecorder = nil
	}
	if m.recordEnabled && m.recordDir != "" {
		trigger := "tui-exec"
		if msg.isCue {
			trigger = "tui-cue-exec"
		}
		if rec, err := NewBatchSessionRecorder(m.recordDir, trigger, m.sshUser, msg.totalJobs); err == nil {
			m.batchRecorder = rec
			if msg.isCue && rec != nil && msg.recipe != nil {
				hash, _ := cuetry.HashRecipeJSON(*msg.recipe)
				rec.RecordRecipeMeta(RecipeMeta{
					RecipePath:        msg.recipePath,
					HostCount:         msg.totalJobs,
					RecipeContentHash: hash,
					StartedAt:         time.Now().UTC(),
				})
			}
		}
	}
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
	if m.batchRecorder != nil {
		m.batchRecorder.RecordHostExecResult(msg.res)
	}
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
	if m.batchRecorder != nil {
		_ = m.batchRecorder.Close()
		m.batchRecorder = nil
	}
	m.execDone = true
	m.execResults = SortHostExecForUI(m.execResults)
	m.clampExecScroll()
	return m, nil
}

func (m *model) handleParallelExecDoneMsg(msg parallelExecDoneMsg) (tea.Model, tea.Cmd) {
	if m.recordEnabled && m.recordDir != "" {
		if rec, err := NewBatchSessionRecorder(m.recordDir, "tui-exec", m.sshUser, len(msg.results)); err == nil {
			for i := range msg.results {
				rec.RecordHostExecResult(msg.results[i])
			}
			_ = rec.Close()
		}
	}
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
	if m.mode == "replaypick" {
		m.clampReplayPickScroll()
	}
	return m, nil
}

func (m *model) handleAgentTransferDoneMsg(msg agentTransferDoneMsg) (tea.Model, tea.Cmd) {
	m.cueResultTitle = msg.title
	if msg.err != "" {
		if strings.TrimSpace(msg.body) != "" {
			m.cueResultBody = msg.body + "\n\nERROR: " + msg.err
		} else {
			m.cueResultBody = "ERROR: " + msg.err
		}
	} else {
		m.cueResultBody = msg.body
	}
	m.execResults = nil
	m.execScroll = 0
	m.execDone = true
	m.resetAgentTransfer()
	m.mode = "execresults"
	return m, nil
}

func (m *model) dispatchKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == "replaypick" {
		return m.updateReplayPickKeys(msg)
	}
	if m.mode == "execresults" {
		return m.updateExecResultsKeys(msg)
	}
	if m.mode == "tunnel" {
		return m.updateTunnelInputs(msg)
	}
	if m.mode == "agenttransferform" {
		return m.updateAgentTransferFormKeys(msg)
	}
	if m.mode == "execinput" || m.mode == "cueexecinput" || m.mode == "filter" {
		return m.updateTextInputMode(msg)
	}
	return m.handleTableKeyMsg(msg)
}

func (m *model) handleTableKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.agentPick != "" {
		switch msg.String() {
		case "esc":
			m.resetAgentTransfer()
			return m, nil
		case "q", "ctrl+c":
			m.resetAgentTransfer()
			m.lastAction = actNone
			return m, tea.Quit
		case "enter":
			return m.agentPickEnter()
		default:
			var cmd tea.Cmd
			m.tbl, cmd = m.tbl.Update(msg)
			return m, cmd
		}
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
	case "R":
		if m.recordDir != "" {
			m.recordEnabled = !m.recordEnabled
		}
		return m, nil
	case "p":
		if strings.TrimSpace(m.recordDir) == "" {
			return m, nil
		}
		names, err := recordings.ListHrecBasenames(m.recordDir)
		if err != nil {
			m.replayListErr = err.Error()
			m.replayFiles = nil
		} else {
			m.replayListErr = ""
			m.replayFiles = names
		}
		m.replayCursor = 0
		m.replayPickScroll = 0
		m.mode = "replaypick"
		m.clampReplayPickScroll()
		return m, nil
	case "enter":
		if r, ok := m.cursorRecord(); ok {
			m.lastAction = tableEnterAction(r)
		} else {
			m.lastAction = actSSH
		}
		return m, tea.Quit
	case "s":
		if r, ok := m.cursorRecord(); ok && truenasSSHKeyAllowed(r) {
			m.lastAction = actSSH
			return m, tea.Quit
		}
		return m, nil
	case "a":
		m.resetAgentTransfer()
		m.agentPick = "source"
		return m, nil
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
		m.ti.Placeholder = "remote shell command (* rows only, or all executable if none marked)"
		m.ti.Reset()
		m.ti.Focus()
		return m, textinput.Blink
	case "r":
		m.mode = "cueexecinput"
		if len(m.availableRecipes) > 0 {
			m.recipeCursor = 0
		}
		m.ti.Reset()
		m.ti.Placeholder = "path/to/recipe.cue (! = execute) — * rows only, or all executable if none marked"
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

func (m *model) updateReplayPickKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = "table"
		m.replayFiles = nil
		m.replayListErr = ""
		m.replayPickScroll = 0
		return m, nil
	case "enter":
		if len(m.replayFiles) == 0 {
			return m, nil
		}
		m.replayFileName = m.replayFiles[m.replayCursor]
		m.lastAction = actReplay
		m.mode = "table"
		m.replayFiles = nil
		m.replayListErr = ""
		m.replayPickScroll = 0
		return m, tea.Quit
	case "up", "k":
		if m.replayCursor > 0 {
			m.replayCursor--
		}
		m.clampReplayPickScroll()
		return m, nil
	case "down", "j":
		if m.replayCursor < len(m.replayFiles)-1 {
			m.replayCursor++
		}
		m.clampReplayPickScroll()
		return m, nil
	case "pgup", "b":
		vis := m.replayPickVisibleLines()
		m.replayPickScroll -= vis
		if m.replayPickScroll < 0 {
			m.replayPickScroll = 0
		}
		m.clampReplayPickScroll()
		return m, nil
	case "pgdown", "f":
		vis := m.replayPickVisibleLines()
		m.replayPickScroll += vis
		m.clampReplayPickScroll()
		return m, nil
	}
	return m, nil
}
