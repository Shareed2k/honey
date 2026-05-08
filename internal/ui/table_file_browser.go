package ui

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shareed2k/honey/internal/hosts"
)

type fileBrowseLoadedMsg struct {
	localPath  string
	remotePath string
	local      []LocalFileEntry
	remote     []RemoteFileEntry
	err        string
}

type fileBrowseCopyDoneMsg struct {
	err string
	msg string
}

func loadFileBrowseCmd(user string, target hosts.Record, localPath, remotePath string, cache *ClientCache) tea.Cmd {
	return func() tea.Msg {
		localResolved, localEntries, err := ListLocalDirUnderRoot(DefaultLocalFilesRoot(), localPath)
		if err != nil {
			return fileBrowseLoadedMsg{err: err.Error()}
		}
		remote := strings.TrimSpace(remotePath)
		if remote == "" {
			remote = "."
		}
		remoteEntries, err := RemoteListDir(user, target, remote, cache)
		if err != nil {
			return fileBrowseLoadedMsg{err: err.Error()}
		}
		return fileBrowseLoadedMsg{
			localPath:  localResolved,
			remotePath: remote,
			local:      localEntries,
			remote:     remoteEntries,
		}
	}
}

func copyLocalToRemoteCmd(user string, target hosts.Record, localPath, remotePath string, cache *ClientCache) tea.Cmd {
	return func() tea.Msg {
		if err := RemoteCopyLocalToRemote(user, target, localPath, remotePath, cache); err != nil {
			return fileBrowseCopyDoneMsg{err: err.Error()}
		}
		return fileBrowseCopyDoneMsg{msg: fmt.Sprintf("uploaded %s → %s", filepath.Base(localPath), remotePath)}
	}
}

func copyRemoteToLocalCmd(user string, target hosts.Record, remotePath, localPath string, cache *ClientCache) tea.Cmd {
	return func() tea.Msg {
		if err := RemoteCopyRemoteToLocal(user, target, remotePath, localPath, cache); err != nil {
			return fileBrowseCopyDoneMsg{err: err.Error()}
		}
		return fileBrowseCopyDoneMsg{msg: fmt.Sprintf("downloaded %s → %s", path.Base(remotePath), localPath)}
	}
}

func remoteParentDir(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "." {
		return "."
	}
	parent := path.Dir(p)
	if parent == "" {
		return "."
	}
	return parent
}

func (m *model) fileBrowseTarget() (hosts.Record, bool) {
	row := m.tbl.Cursor()
	if row < 0 || row >= len(m.visible) {
		return hosts.Record{}, false
	}
	realIdx := m.visible[row]
	if realIdx < 0 || realIdx >= len(m.recs) {
		return hosts.Record{}, false
	}
	return m.recs[realIdx], true
}

func (m *model) updateFileBrowse(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = "table"
		return m, nil
	case "tab":
		if m.fileFocus == "local" {
			m.fileFocus = "remote"
		} else {
			m.fileFocus = "local"
		}
		return m, nil
	case "left", "h":
		m.fileFocus = "local"
		return m, nil
	case "right", "l":
		m.fileFocus = "remote"
		return m, nil
	case "up", "k":
		if m.fileFocus == "local" {
			if m.fileLocalCursor > 0 {
				m.fileLocalCursor--
			}
		} else {
			if m.fileRemoteCursor > 0 {
				m.fileRemoteCursor--
			}
		}
		return m, nil
	case "down", "j":
		if m.fileFocus == "local" {
			if m.fileLocalCursor < len(m.fileLocalEntries)-1 {
				m.fileLocalCursor++
			}
		} else {
			if m.fileRemoteCursor < len(m.fileRemoteEntries)-1 {
				m.fileRemoteCursor++
			}
		}
		return m, nil
	case "backspace", "u":
		if rec, ok := m.fileBrowseTarget(); ok {
			if m.fileFocus == "local" {
				next := filepath.Dir(m.fileLocalPath)
				return m, loadFileBrowseCmd(m.sshUser, rec, next, m.fileRemotePath, m.fileClientCache)
			}
			return m, loadFileBrowseCmd(m.sshUser, rec, m.fileLocalPath, remoteParentDir(m.fileRemotePath), m.fileClientCache)
		}
		return m, nil
	case "enter":
		if rec, ok := m.fileBrowseTarget(); ok {
			if m.fileFocus == "local" {
				if len(m.fileLocalEntries) == 0 {
					return m, nil
				}
				selected := m.fileLocalEntries[m.fileLocalCursor]
				if !selected.IsDir {
					return m, nil
				}
				return m, loadFileBrowseCmd(m.sshUser, rec, selected.Path, m.fileRemotePath, m.fileClientCache)
			}
			if len(m.fileRemoteEntries) == 0 {
				return m, nil
			}
			selected := m.fileRemoteEntries[m.fileRemoteCursor]
			if !selected.IsDir {
				return m, nil
			}
			return m, loadFileBrowseCmd(m.sshUser, rec, m.fileLocalPath, selected.Path, m.fileClientCache)
		}
		return m, nil
	case ">":
		if rec, ok := m.fileBrowseTarget(); ok {
			if len(m.fileLocalEntries) == 0 {
				return m, nil
			}
			selected := m.fileLocalEntries[m.fileLocalCursor]
			if selected.IsDir {
				m.fileStatus = "select a local file (not directory) to upload"
				return m, nil
			}
			targetRemote := path.Join(m.fileRemotePath, selected.Name)
			return m, copyLocalToRemoteCmd(m.sshUser, rec, selected.Path, targetRemote, m.fileClientCache)
		}
		return m, nil
	case "<":
		if rec, ok := m.fileBrowseTarget(); ok {
			if len(m.fileRemoteEntries) == 0 {
				return m, nil
			}
			selected := m.fileRemoteEntries[m.fileRemoteCursor]
			if selected.IsDir {
				m.fileStatus = "select a remote file (not directory) to download"
				return m, nil
			}
			targetLocal := filepath.Join(m.fileLocalPath, selected.Name)
			return m, copyRemoteToLocalCmd(m.sshUser, rec, selected.Path, targetLocal, m.fileClientCache)
		}
		return m, nil
	case "r":
		if rec, ok := m.fileBrowseTarget(); ok {
			return m, loadFileBrowseCmd(m.sshUser, rec, m.fileLocalPath, m.fileRemotePath, m.fileClientCache)
		}
		return m, nil
	default:
		return m, nil
	}
}

func (m *model) viewFileBrowse(helpStyle lipgloss.Style) string {
	title := lipgloss.NewStyle().Bold(true).Render("File browser (dual pane)")
	leftLines := []string{lipgloss.NewStyle().Bold(m.fileFocus == "local").Render("Local"), m.fileLocalPath, ""}
	if len(m.fileLocalEntries) == 0 {
		leftLines = append(leftLines, "(empty)")
	}
	for i, e := range m.fileLocalEntries {
		cursor := "  "
		if m.fileFocus == "local" && i == m.fileLocalCursor {
			cursor = "> "
		}
		marker := " "
		if e.IsDir {
			marker = "d"
		}
		leftLines = append(leftLines, fmt.Sprintf("%s[%s] %s", cursor, marker, e.Name))
	}

	rightLines := []string{lipgloss.NewStyle().Bold(m.fileFocus == "remote").Render("Remote"), m.fileRemotePath, ""}
	if len(m.fileRemoteEntries) == 0 {
		rightLines = append(rightLines, "(empty)")
	}
	for i, e := range m.fileRemoteEntries {
		cursor := "  "
		if m.fileFocus == "remote" && i == m.fileRemoteCursor {
			cursor = "> "
		}
		marker := " "
		if e.IsDir {
			marker = "d"
		}
		rightLines = append(rightLines, fmt.Sprintf("%s[%s] %s", cursor, marker, e.Name))
	}
	left := strings.Join(leftLines, "\n")
	right := strings.Join(rightLines, "\n")
	panes := lipgloss.JoinHorizontal(
		lipgloss.Top,
		lipgloss.NewStyle().Width((m.winW/2)-4).Render(left),
		"  ",
		lipgloss.NewStyle().Width((m.winW/2)-4).Render(right),
	)
	status := m.fileStatus
	if strings.TrimSpace(status) == "" {
		status = "ready"
	}
	help := helpStyle.Render("tab/←/→: focus pane   enter: open dir   u/backspace: parent   > upload   < download   r refresh   esc back")
	return title + "\n" + baseStyle.Render(panes) + "\n" + helpStyle.Render(status) + "\n" + help
}
