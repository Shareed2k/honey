package ui

import "github.com/shareed2k/honey/internal/cuetry"

func wrapSteps(ss ...cuetry.Step) []cuetry.StepWrapper {
	out := make([]cuetry.StepWrapper, len(ss))
	for i, s := range ss {
		out[i] = cuetry.StepWrapper{Step: s}
	}
	return out
}
