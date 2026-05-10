package ui

import (
	"golang.org/x/term"
)

func termIsTerminal(fd int) bool {
	return term.IsTerminal(fd)
}

func termMakeRaw(fd int) (*term.State, error) {
	return term.MakeRaw(fd)
}

func termRestore(fd int, state *term.State) error {
	if state == nil {
		return nil
	}
	return term.Restore(fd, state)
}

func termGetSize(fd int) (width, height int, err error) {
	return term.GetSize(fd)
}
