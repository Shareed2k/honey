// Package guardrails implements the pure, standalone guardrail rule engine:
// operator-defined pattern rules that inspect a command or SQL query text and
// either deny (hard block) or warn (allow, with a message) on a match. This
// package knows nothing about where rules come from or how a Verdict is
// enforced; it only compiles rules once and evaluates text against them.
package guardrails

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Action is the effect a matched rule has on the evaluated action.
type Action string

// Kind identifies the category of text a rule inspects.
type Kind string

const (
	// ActionDeny hard-blocks the evaluated action.
	ActionDeny Action = "deny"
	// ActionWarn allows the evaluated action but surfaces a message.
	ActionWarn Action = "warn"

	// KindCommand marks a rule/evaluation as applying to shell command text.
	KindCommand Kind = "command"
	// KindSQL marks a rule/evaluation as applying to SQL query text.
	KindSQL Kind = "sql"
	// KindAny marks a rule as applying to every kind of text.
	KindAny Kind = "any"
)

// Bounds enforced by NewRuleset and Evaluate.
const (
	// MaxRules caps the number of rules a single Ruleset may hold.
	MaxRules = 512
	// MaxPatternLen caps the length, in bytes, of any single Pattern or
	// Absent regex source.
	MaxPatternLen = 1024
	// MaxScanBytes caps how much of the evaluated text Evaluate scans; text
	// longer than this is truncated to the first MaxScanBytes before matching.
	MaxScanBytes = 1 << 20 // 1 MiB
)

// ErrInvalidRule is wrapped, naming the offending rule, by every validation
// error NewRuleset returns.
var ErrInvalidRule = errors.New("invalid guardrail rule")

// Rule is one operator-defined guardrail (as authored in config / the UI).
type Rule struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Action      Action   `json:"action"`             // default "deny" when empty
	AppliesTo   Kind     `json:"applies_to"`         // default "any" when empty
	Words       []string `json:"words,omitempty"`    // case-insensitive literal substrings; ANY match
	Patterns    []string `json:"patterns,omitempty"` // RE2 regexes; ANY match
	Absent      []string `json:"absent,omitempty"`   // RE2 regexes that must NOT match (negation without lookahead)
	Message     string   `json:"message,omitempty"`  // shown/audited on match
	Targets     []string `json:"targets,omitempty"`  // provider/group/name globs; empty = all resources
}

// Attrs describes the resource an action targets, for Targets scoping.
type Attrs struct {
	Provider string
	Groups   []string
	Name     string
}

// Verdict is the result of evaluating text against a Ruleset.
type Verdict struct {
	Denied   bool     // a deny rule matched
	Reason   string   // the matched deny rule's Message (or a default)
	Rule     string   // name of the matched deny rule
	Warnings []string // messages of all matched warn rules (when not denied)
}

// compiledRule is a Rule after validation and compilation: patterns are
// compiled regexes, words are lowercased once, and targets are pre-validated
// glob sources. It is never mutated after NewRuleset builds it.
type compiledRule struct {
	name         string
	action       Action
	appliesTo    Kind
	loweredWords []string
	patterns     []*regexp.Regexp
	absents      []*regexp.Regexp
	targets      []string
	message      string
}

// Ruleset is an immutable, compiled set of rules. NewRuleset builds it once
// and Evaluate never mutates it afterward, so a *Ruleset is safe for
// concurrent use by multiple goroutines without a mutex.
type Ruleset struct {
	rules []compiledRule
}

// NewRuleset validates and compiles rules once. It returns ErrInvalidRule
// (wrapped, naming the offending rule) when: there are more than MaxRules
// rules; a rule's Name is empty; a rule has zero Words and zero Patterns; a
// Pattern or Absent regex fails regexp.Compile or exceeds MaxPatternLen; a
// rule's Action is set but not one of {deny, warn}; a rule's AppliesTo is set
// but not one of {command, sql, any}; or a rule's Targets contains a
// malformed glob. A nil or empty rules slice yields a valid, empty Ruleset.
func NewRuleset(rules []Rule) (*Ruleset, error) {
	if len(rules) > MaxRules {
		return nil, fmt.Errorf("%w: %d rules exceeds max %d", ErrInvalidRule, len(rules), MaxRules)
	}
	compiled := make([]compiledRule, 0, len(rules))
	for _, r := range rules {
		cr, err := compileRule(r)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, cr)
	}
	return &Ruleset{rules: compiled}, nil
}

// compileRule validates and compiles a single Rule, applying the Action and
// AppliesTo defaults documented on NewRuleset.
func compileRule(r Rule) (compiledRule, error) {
	name := r.Name
	if name == "" {
		return compiledRule{}, fmt.Errorf("%w: rule name is empty", ErrInvalidRule)
	}

	action := r.Action
	if action == "" {
		action = ActionDeny
	}
	if action != ActionDeny && action != ActionWarn {
		return compiledRule{}, fmt.Errorf("%w: rule %q: invalid action %q", ErrInvalidRule, name, r.Action)
	}

	appliesTo := r.AppliesTo
	if appliesTo == "" {
		appliesTo = KindAny
	}
	if appliesTo != KindCommand && appliesTo != KindSQL && appliesTo != KindAny {
		return compiledRule{}, fmt.Errorf("%w: rule %q: invalid applies_to %q", ErrInvalidRule, name, r.AppliesTo)
	}

	if len(r.Words) == 0 && len(r.Patterns) == 0 {
		return compiledRule{}, fmt.Errorf("%w: rule %q: no words and no patterns", ErrInvalidRule, name)
	}

	loweredWords := make([]string, 0, len(r.Words))
	for _, w := range r.Words {
		loweredWords = append(loweredWords, strings.ToLower(w))
	}

	patterns, err := compilePatterns(name, "pattern", r.Patterns)
	if err != nil {
		return compiledRule{}, err
	}
	absents, err := compilePatterns(name, "absent", r.Absent)
	if err != nil {
		return compiledRule{}, err
	}

	for _, g := range r.Targets {
		if !validGlob(g) {
			return compiledRule{}, fmt.Errorf("%w: rule %q: invalid target glob %q", ErrInvalidRule, name, g)
		}
	}

	return compiledRule{
		name:         name,
		action:       action,
		appliesTo:    appliesTo,
		loweredWords: loweredWords,
		patterns:     patterns,
		absents:      absents,
		targets:      append([]string(nil), r.Targets...),
		message:      r.Message,
	}, nil
}

// compilePatterns compiles each regex source in pats, enforcing MaxPatternLen
// and wrapping any regexp.Compile error with the rule name and field for
// context.
func compilePatterns(ruleName, field string, pats []string) ([]*regexp.Regexp, error) {
	if len(pats) == 0 {
		return nil, nil
	}
	out := make([]*regexp.Regexp, 0, len(pats))
	for _, p := range pats {
		if len(p) > MaxPatternLen {
			return nil, fmt.Errorf("%w: rule %q: %s %q exceeds max length %d", ErrInvalidRule, ruleName, field, p, MaxPatternLen)
		}
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("%w: rule %q: compile %s %q: %v", ErrInvalidRule, ruleName, field, p, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// validGlob reports whether g is a syntactically valid path.Match pattern.
func validGlob(g string) bool {
	_, err := path.Match(g, "")
	return err == nil
}

// Empty reports whether the ruleset carries no rules.
func (rs *Ruleset) Empty() bool {
	return len(rs.rules) == 0
}

// Evaluate checks text (a command or SQL string) against every applicable
// rule, in ruleset order. text longer than MaxScanBytes is truncated to the
// first MaxScanBytes before matching.
//
// A rule applies when kind matches its AppliesTo (either is KindAny, or they
// are equal) and, when the rule has Targets, at least one Targets glob
// matches target.Provider, target.Name, or any of target.Groups (empty
// Targets applies to every resource).
//
// A rule matches when any Word is a case-insensitive substring of text, or
// any Pattern matches text, AND none of its Absent regexes match text.
//
// The first applicable deny rule (in ruleset order) that matches wins:
// Evaluate returns immediately with Verdict.Denied true, Verdict.Reason set
// to that rule's Message (or "blocked by guardrail "+name when Message is
// empty), and Verdict.Rule set to its name. Otherwise Evaluate collects the
// Message of every matched warn rule into Verdict.Warnings, in ruleset order
// with duplicates removed (using "matched guardrail "+name when a warn rule's
// Message is empty).
func (rs *Ruleset) Evaluate(text string, kind Kind, target Attrs) Verdict {
	if rs.Empty() {
		return Verdict{}
	}
	if len(text) > MaxScanBytes {
		text = text[:MaxScanBytes]
	}
	lowered := strings.ToLower(text)

	var warnings []string
	seen := make(map[string]struct{})

	for _, r := range rs.rules {
		if !r.appliesToKind(kind) || !r.appliesToTarget(target) {
			continue
		}
		if !r.matches(text, lowered) {
			continue
		}
		if r.action == ActionDeny {
			reason := r.message
			if reason == "" {
				reason = "blocked by guardrail " + r.name
			}
			return Verdict{Denied: true, Reason: reason, Rule: r.name}
		}
		msg := r.message
		if msg == "" {
			msg = "matched guardrail " + r.name
		}
		if _, ok := seen[msg]; ok {
			continue
		}
		seen[msg] = struct{}{}
		warnings = append(warnings, msg)
	}
	return Verdict{Warnings: warnings}
}

// appliesToKind reports whether this rule applies to text of the given kind:
// true when either side is KindAny, or the two are equal.
func (r *compiledRule) appliesToKind(kind Kind) bool {
	return r.appliesTo == KindAny || kind == KindAny || r.appliesTo == kind
}

// appliesToTarget reports whether this rule applies to target: true when the
// rule has no Targets, or any Targets glob matches target.Provider,
// target.Name, or any of target.Groups.
func (r *compiledRule) appliesToTarget(target Attrs) bool {
	if len(r.targets) == 0 {
		return true
	}
	for _, g := range r.targets {
		if globMatches(g, target.Provider) || globMatches(g, target.Name) {
			return true
		}
		for _, group := range target.Groups {
			if globMatches(g, group) {
				return true
			}
		}
	}
	return false
}

// globMatches reports whether candidate matches the glob g. Targets globs are
// validated at compile time (validGlob), so an error here can only mean
// candidate itself is malformed as a path.Match name, which never happens for
// the plain resource identifiers this package matches against; treat that
// case as no match rather than panicking.
func globMatches(g, candidate string) bool {
	if candidate == "" {
		return false
	}
	ok, err := path.Match(g, candidate)
	return err == nil && ok
}

// matches reports whether the rule's Words or Patterns match, and none of its
// Absent regexes match. lowered must be strings.ToLower(text).
func (r *compiledRule) matches(text, lowered string) bool {
	matched := false
	for _, w := range r.loweredWords {
		if strings.Contains(lowered, w) {
			matched = true
			break
		}
	}
	if !matched {
		for _, p := range r.patterns {
			if p.MatchString(text) {
				matched = true
				break
			}
		}
	}
	if !matched {
		return false
	}
	for _, a := range r.absents {
		if a.MatchString(text) {
			return false
		}
	}
	return true
}
