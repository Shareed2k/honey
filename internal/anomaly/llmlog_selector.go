// Package anomaly provides log anomaly detection structures and algorithms.
//
// LLMLog (Large Language Model-based Log Template Generation via LLM-driven Multi-Round Annotation)
// is based on the greedy set-cover dynamic demonstration selection algorithm (Algorithm 3) proposed in:
// "LLMLog: Advanced Log Template Generation via LLM-driven Multi-Round Annotation" (VLDB 2025)
// by Fei Teng, Haoyang Li, and Lei Chen.
// Licensed under Creative Commons Attribution-NonCommercial-NoDerivatives 4.0 International (CC BY-NC-ND 4.0).
// For license details, see: https://creativecommons.org/licenses/by-nc-nd/4.0/
package anomaly

import (
	"strings"
)

// SelectDemonstrations returns the best matching few-shot examples for the target tokens.
// The selection follows a greedy set-cover logic to maximize target token coverage.
func SelectDemonstrations(targetTokens []string, pool []DemoInstance, maxDemos int) []DemoInstance {
	if len(targetTokens) == 0 || len(pool) == 0 || maxDemos <= 0 {
		return nil
	}

	// Keep track of which target tokens are covered.
	// We use target token indices because target tokens can have duplicates
	// and we want to track coverage of specific target token positions.
	covered := make([]bool, len(targetTokens))

	var selected []DemoInstance
	usedPool := make([]bool, len(pool))

	for iter := 0; iter < maxDemos; iter++ {
		bestIdx := -1
		maxGain := 0
		var bestNewCovered []int

		for i, demo := range pool {
			if usedPool[i] {
				continue
			}

			// Tokenize on-the-fly if Tokens is empty but Template is present,
			// without modifying the shared pool source to remain thread-safe.
			demoTokens := demo.Tokens
			if len(demoTokens) == 0 && demo.Template != "" {
				demoTokens = tokenize(demo.Template)
			}

			// Count how many uncovered target tokens match any of the demo's tokens
			gain := 0
			var newCovered []int

			for tgtIdx, tgtToken := range targetTokens {
				if covered[tgtIdx] {
					continue
				}

				// Check if tgtToken matches any token in the demo
				match := false
				for _, demoToken := range demoTokens {
					if tokenMatches(tgtToken, demoToken) {
						match = true
						break
					}
				}

				if match {
					gain++
					newCovered = append(newCovered, tgtIdx)
				}
			}

			if gain > maxGain {
				maxGain = gain
				bestIdx = i
				bestNewCovered = newCovered
			}
		}

		// If the best candidate has a gain of 0, break
		if maxGain == 0 || bestIdx == -1 {
			break
		}

		// Mark the best candidate as used
		usedPool[bestIdx] = true

		// Append the selected DemoInstance to the results
		selected = append(selected, pool[bestIdx])

		// Mark all of its matching target tokens as covered
		for _, tgtIdx := range bestNewCovered {
			covered[tgtIdx] = true
		}
	}

	return selected
}

// SelectDefaultDemonstrations is a thread-safe wrapper helper that automatically
// acquires PoolMu.RLock() before calling SelectDemonstrations on DefaultSeedPool.
func SelectDefaultDemonstrations(targetTokens []string, maxDemos int) []DemoInstance {
	PoolMu.RLock()
	defer PoolMu.RUnlock()
	return SelectDemonstrations(targetTokens, DefaultSeedPool, maxDemos)
}

// tokenMatches checks if two tokens match.
// It checks if they are equal, or if they are identical case-insensitively,
// or if they are both wildcards "<*>".
func tokenMatches(a, b string) bool {
	if a == b {
		return true
	}
	if strings.EqualFold(a, b) {
		return true
	}
	if a == "<*>" && b == "<*>" {
		return true
	}
	return false
}
