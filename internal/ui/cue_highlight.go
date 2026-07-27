package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// CUE recipe syntax highlighting for the TUI recipe preview. Pure string→string
// (no TUI/model state), extracted from table.go so it can be read and tested in
// isolation.

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
