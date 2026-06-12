package cuetry

func wrap(s Step) StepWrapper { return StepWrapper{Step: s} }

func wrapAll(ss ...Step) []StepWrapper {
	out := make([]StepWrapper, len(ss))
	for i, s := range ss {
		out[i] = StepWrapper{Step: s}
	}
	return out
}
