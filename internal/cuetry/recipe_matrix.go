package cuetry

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ExpandMatrixSteps evaluates matrix definitions and replaces them with Cartesian expanded steps.
func ExpandMatrixSteps(r *Recipe) error {
	var newSteps []StepWrapper
	r.MatrixExpansions = make(map[string][]string)

	for i, w := range r.Steps {
		b := w.Step.Base()
		if len(b.Matrix) == 0 {
			newSteps = append(newSteps, w)
			continue
		}

		if strings.TrimSpace(b.ID) == "" {
			return fmt.Errorf("step %d has matrix but no id", i)
		}

		keys := make([]string, 0, len(b.Matrix))
		for k := range b.Matrix {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var combos []map[string]string
		combos = append(combos, make(map[string]string))

		for _, key := range keys {
			vals := b.Matrix[key]
			var nextCombos []map[string]string
			for _, c := range combos {
				for _, val := range vals {
					nc := make(map[string]string, len(c)+1)
					for k, v := range c {
						nc[k] = v
					}
					nc[key] = val
					nextCombos = append(nextCombos, nc)
				}
			}
			combos = nextCombos
		}

		var expandedIDs []string
		for _, combo := range combos {
			raw, err := json.Marshal(w)
			if err != nil {
				return err
			}
			var cloneWrapper StepWrapper
			if err := json.Unmarshal(raw, &cloneWrapper); err != nil {
				return err
			}

			cb := cloneWrapper.Step.Base()
			cb.Matrix = nil
			if cb.Env == nil {
				cb.Env = make(map[string]string)
			}
			var parts []string
			for _, k := range keys {
				cb.Env[k] = combo[k]
				parts = append(parts, fmt.Sprintf("%s=%s", k, combo[k]))
			}
			newID := fmt.Sprintf("%s[%s]", b.ID, strings.Join(parts, ","))
			cb.ID = newID
			expandedIDs = append(expandedIDs, newID)
			newSteps = append(newSteps, cloneWrapper)
		}
		r.MatrixExpansions[b.ID] = expandedIDs
	}

	// Update dependencies
	for _, w := range newSteps {
		b := w.Step.Base()
		var newDeps []string
		for _, dep := range b.Depends {
			if expanded, ok := r.MatrixExpansions[dep]; ok {
				newDeps = append(newDeps, expanded...)
			} else {
				newDeps = append(newDeps, dep)
			}
		}
		b.Depends = newDeps
	}

	r.Steps = newSteps
	return nil
}
