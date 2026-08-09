package guardrails

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestNewRuleset_EmptyInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		rules []Rule
	}{
		{name: "nil slice", rules: nil},
		{name: "empty slice", rules: []Rule{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rs, err := NewRuleset(tt.rules)
			require.NoError(t, err)
			require.NotNil(t, rs)
			assert.True(t, rs.Empty())

			got := rs.Evaluate("rm -rf /", KindCommand, Attrs{})
			assert.Equal(t, Verdict{}, got)
		})
	}
}

func TestEvaluate_WordMatch(t *testing.T) {
	t.Parallel()

	rs, err := NewRuleset([]Rule{
		{Name: "no-drop", Words: []string{"DROP TABLE"}, Message: "dropping tables is forbidden"},
	})
	require.NoError(t, err)
	assert.False(t, rs.Empty())

	tests := []struct {
		name       string
		text       string
		wantDenied bool
	}{
		{name: "exact case", text: "DROP TABLE users", wantDenied: true},
		{name: "lower case", text: "drop table users", wantDenied: true},
		{name: "mixed case substring", text: "please Drop Table users now", wantDenied: true},
		{name: "no match", text: "select * from users", wantDenied: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := rs.Evaluate(tt.text, KindSQL, Attrs{})
			assert.Equal(t, tt.wantDenied, got.Denied)
			if tt.wantDenied {
				assert.Equal(t, "no-drop", got.Rule)
				assert.Equal(t, "dropping tables is forbidden", got.Reason)
			}
		})
	}
}

func TestEvaluate_PatternMatch(t *testing.T) {
	t.Parallel()

	rs, err := NewRuleset([]Rule{
		{Name: "no-rm-rf", Patterns: []string{`rm\s+-rf\s+/`}, Message: "recursive root delete blocked"},
	})
	require.NoError(t, err)

	got := rs.Evaluate("sudo rm -rf /", KindCommand, Attrs{})
	require.True(t, got.Denied)
	assert.Equal(t, "no-rm-rf", got.Rule)
	assert.Equal(t, "recursive root delete blocked", got.Reason)

	got = rs.Evaluate("rm -rf ./build", KindCommand, Attrs{})
	assert.False(t, got.Denied)
}

func TestEvaluate_AbsentSuppression(t *testing.T) {
	t.Parallel()

	rs, err := NewRuleset([]Rule{
		{
			Name:     "write-without-where",
			Patterns: []string{`(?is)\b(?:update|delete)\b`},
			Absent:   []string{`(?is)\bwhere\b`},
			Message:  "write without a where clause",
		},
	})
	require.NoError(t, err)

	tests := []struct {
		name       string
		text       string
		wantDenied bool
	}{
		{name: "pattern matches, absent also matches: no match", text: "UPDATE t SET x=1 WHERE id=2", wantDenied: false},
		{name: "pattern matches, absent absent: matches", text: "UPDATE t SET x=1", wantDenied: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := rs.Evaluate(tt.text, KindSQL, Attrs{})
			assert.Equal(t, tt.wantDenied, got.Denied)
		})
	}
}

func TestEvaluate_DenyPrecedence(t *testing.T) {
	t.Parallel()

	rs, err := NewRuleset([]Rule{
		{Name: "warn-select-star", Action: ActionWarn, Words: []string{"select *"}, Message: "avoid select star"},
		{Name: "deny-drop", Action: ActionDeny, Words: []string{"drop table"}, Message: "drop table is forbidden"},
	})
	require.NoError(t, err)

	got := rs.Evaluate("select * from t; drop table t", KindSQL, Attrs{})
	require.True(t, got.Denied)
	assert.Equal(t, "deny-drop", got.Rule)
	assert.Equal(t, "drop table is forbidden", got.Reason)
	assert.Empty(t, got.Warnings)
}

func TestEvaluate_DenyPrecedence_FirstInOrder(t *testing.T) {
	t.Parallel()

	rs, err := NewRuleset([]Rule{
		{Name: "deny-first", Action: ActionDeny, Words: []string{"secret"}, Message: "first deny"},
		{Name: "deny-second", Action: ActionDeny, Words: []string{"secret"}, Message: "second deny"},
	})
	require.NoError(t, err)

	got := rs.Evaluate("this has a secret in it", KindAny, Attrs{})
	require.True(t, got.Denied)
	assert.Equal(t, "deny-first", got.Rule)
	assert.Equal(t, "first deny", got.Reason)
}

func TestEvaluate_WarnOnly(t *testing.T) {
	t.Parallel()

	rs, err := NewRuleset([]Rule{
		{Name: "warn-a", Action: ActionWarn, Words: []string{"foo"}, Message: "warned about foo"},
		{Name: "warn-b", Action: ActionWarn, Words: []string{"bar"}, Message: "warned about bar"},
		{Name: "warn-c", Action: ActionWarn, Words: []string{"nope"}},
		{Name: "warn-dup", Action: ActionWarn, Words: []string{"foo"}, Message: "warned about foo"},
	})
	require.NoError(t, err)

	got := rs.Evaluate("foo and bar together", KindAny, Attrs{})
	assert.False(t, got.Denied)
	assert.Empty(t, got.Rule)
	assert.Equal(t, []string{"warned about foo", "warned about bar"}, got.Warnings)
}

func TestEvaluate_WarnOnly_DefaultMessage(t *testing.T) {
	t.Parallel()

	rs, err := NewRuleset([]Rule{
		{Name: "warn-no-message", Action: ActionWarn, Words: []string{"foo"}},
	})
	require.NoError(t, err)

	got := rs.Evaluate("foo bar", KindAny, Attrs{})
	assert.False(t, got.Denied)
	assert.Equal(t, []string{"matched guardrail warn-no-message"}, got.Warnings)
}

func TestEvaluate_DenyDefaultMessage(t *testing.T) {
	t.Parallel()

	rs, err := NewRuleset([]Rule{
		{Name: "deny-no-message", Words: []string{"foo"}},
	})
	require.NoError(t, err)

	got := rs.Evaluate("foo bar", KindAny, Attrs{})
	require.True(t, got.Denied)
	assert.Equal(t, "blocked by guardrail deny-no-message", got.Reason)
	assert.Equal(t, "deny-no-message", got.Rule)
}

func TestEvaluate_AppliesToFiltering(t *testing.T) {
	t.Parallel()

	rs, err := NewRuleset([]Rule{
		{Name: "sql-only", AppliesTo: KindSQL, Words: []string{"drop"}},
		{Name: "command-only", AppliesTo: KindCommand, Words: []string{"drop"}},
		{Name: "any-kind", AppliesTo: KindAny, Words: []string{"drop"}},
	})
	require.NoError(t, err)

	tests := []struct {
		name       string
		kind       Kind
		wantRule   string
		wantDenied bool
	}{
		{name: "sql text hits sql-only first", kind: KindSQL, wantRule: "sql-only", wantDenied: true},
		{name: "command text hits command-only first", kind: KindCommand, wantRule: "command-only", wantDenied: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := rs.Evaluate("drop it", tt.kind, Attrs{})
			require.Equal(t, tt.wantDenied, got.Denied)
			assert.Equal(t, tt.wantRule, got.Rule)
		})
	}

	// A ruleset with only a single-kind rule must not fire for the other kind.
	rs2, err := NewRuleset([]Rule{{Name: "sql-only", AppliesTo: KindSQL, Words: []string{"drop"}}})
	require.NoError(t, err)

	assert.False(t, rs2.Evaluate("drop it", KindCommand, Attrs{}).Denied, "sql-only rule must not fire for command kind")
	assert.True(t, rs2.Evaluate("drop it", KindSQL, Attrs{}).Denied, "sql-only rule must fire for sql kind")

	rs3, err := NewRuleset([]Rule{{Name: "command-only", AppliesTo: KindCommand, Words: []string{"drop"}}})
	require.NoError(t, err)

	assert.False(t, rs3.Evaluate("drop it", KindSQL, Attrs{}).Denied, "command-only rule must not fire for sql kind")
	assert.True(t, rs3.Evaluate("drop it", KindCommand, Attrs{}).Denied, "command-only rule must fire for command kind")

	rs4, err := NewRuleset([]Rule{{Name: "any-kind", Words: []string{"drop"}}}) // AppliesTo defaults to any
	require.NoError(t, err)

	assert.True(t, rs4.Evaluate("drop it", KindSQL, Attrs{}).Denied, "any-kind rule must fire for sql kind")
	assert.True(t, rs4.Evaluate("drop it", KindCommand, Attrs{}).Denied, "any-kind rule must fire for command kind")
}

func TestEvaluate_Targets(t *testing.T) {
	t.Parallel()

	rs, err := NewRuleset([]Rule{
		{Name: "prod-k8s-only", Targets: []string{"k8s", "prod-*"}, Words: []string{"drop"}},
	})
	require.NoError(t, err)

	tests := []struct {
		name       string
		target     Attrs
		wantDenied bool
	}{
		{name: "matches provider glob", target: Attrs{Provider: "k8s"}, wantDenied: true},
		{name: "matches name glob", target: Attrs{Provider: "docker", Name: "prod-db"}, wantDenied: true},
		{name: "matches group glob", target: Attrs{Provider: "docker", Groups: []string{"prod-x"}}, wantDenied: true},
		{name: "no match on unrelated resource", target: Attrs{Provider: "docker", Name: "staging-db", Groups: []string{"dev"}}, wantDenied: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := rs.Evaluate("drop it", KindAny, tt.target)
			assert.Equal(t, tt.wantDenied, got.Denied)
		})
	}
}

func TestEvaluate_Targets_EmptyMatchesAll(t *testing.T) {
	t.Parallel()

	rs, err := NewRuleset([]Rule{{Name: "global", Words: []string{"drop"}}})
	require.NoError(t, err)

	assert.True(t, rs.Evaluate("drop it", KindAny, Attrs{Provider: "anything", Name: "whatever"}).Denied)
	assert.True(t, rs.Evaluate("drop it", KindAny, Attrs{}).Denied)
}

func TestNewRuleset_ValidationErrors(t *testing.T) {
	t.Parallel()

	longPattern := "(" + strings.Repeat("a", MaxPatternLen) + ")"

	manyRules := make([]Rule, MaxRules+1)
	for i := range manyRules {
		manyRules[i] = Rule{Name: "r", Words: []string{"x"}}
	}

	tests := []struct {
		name  string
		rules []Rule
	}{
		{
			name:  "empty name",
			rules: []Rule{{Name: "", Words: []string{"x"}}},
		},
		{
			name:  "no words and no patterns",
			rules: []Rule{{Name: "empty-rule"}},
		},
		{
			name:  "bad regex pattern",
			rules: []Rule{{Name: "bad-pattern", Patterns: []string{"("}}},
		},
		{
			name:  "bad regex absent",
			rules: []Rule{{Name: "bad-absent", Words: []string{"x"}, Absent: []string{"("}}},
		},
		{
			name:  "pattern exceeds max length",
			rules: []Rule{{Name: "too-long", Patterns: []string{longPattern}}},
		},
		{
			name:  "too many rules",
			rules: manyRules,
		},
		{
			name:  "bad action",
			rules: []Rule{{Name: "bad-action", Action: "allow", Words: []string{"x"}}},
		},
		{
			name:  "bad applies_to",
			rules: []Rule{{Name: "bad-applies", AppliesTo: "network", Words: []string{"x"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rs, err := NewRuleset(tt.rules)
			require.Error(t, err)
			assert.Nil(t, rs)
			assert.True(t, errors.Is(err, ErrInvalidRule), "error %v should wrap ErrInvalidRule", err)
		})
	}
}

func TestNewRuleset_ValidActionsAndAppliesTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		action    Action
		appliesTo Kind
	}{
		{name: "empty action defaults to deny", action: "", appliesTo: KindAny},
		{name: "explicit deny", action: ActionDeny, appliesTo: KindAny},
		{name: "explicit warn", action: ActionWarn, appliesTo: KindAny},
		{name: "empty applies_to defaults to any", action: ActionDeny, appliesTo: ""},
		{name: "explicit command", action: ActionDeny, appliesTo: KindCommand},
		{name: "explicit sql", action: ActionDeny, appliesTo: KindSQL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewRuleset([]Rule{{Name: "r", Action: tt.action, AppliesTo: tt.appliesTo, Words: []string{"x"}}})
			require.NoError(t, err)
		})
	}
}

func TestEvaluate_MaxScanBytesTruncation(t *testing.T) {
	t.Parallel()

	rs, err := NewRuleset([]Rule{{Name: "late-word", Words: []string{"needle"}}})
	require.NoError(t, err)

	padding := strings.Repeat("a", MaxScanBytes)
	textWithNeedleAfterCap := padding + "needle"

	got := rs.Evaluate(textWithNeedleAfterCap, KindAny, Attrs{})
	assert.False(t, got.Denied, "needle beyond MaxScanBytes must not be found")

	textWithNeedleBeforeCap := "needle" + padding
	got = rs.Evaluate(textWithNeedleBeforeCap, KindAny, Attrs{})
	assert.True(t, got.Denied, "needle within MaxScanBytes must be found")
}

// TestSQLRecipes exercises the five reference SQL guardrail recipes as
// fixtures: each is compiled into its own single-rule Ruleset and evaluated
// against a matching and (where applicable) a non-matching sample query.
func TestSQLRecipes(t *testing.T) {
	t.Parallel()

	t.Run("block-tautology-where", func(t *testing.T) {
		t.Parallel()

		rs, err := NewRuleset([]Rule{{
			Name:     "block-tautology-where",
			Action:   ActionDeny,
			Patterns: []string{`(?is)\bwhere\s+\(*\s*(?:1\s*=\s*1|'1'\s*=\s*'1'|true)\s*\)*\s*(?:;|$|\breturning\b)`},
		}})
		require.NoError(t, err)

		got := rs.Evaluate("DELETE FROM t WHERE 1=1;", KindSQL, Attrs{})
		assert.True(t, got.Denied)

		got = rs.Evaluate("DELETE FROM t WHERE id=5;", KindSQL, Attrs{})
		assert.False(t, got.Denied)
	})

	t.Run("block-write-with-subquery", func(t *testing.T) {
		t.Parallel()

		rs, err := NewRuleset([]Rule{{
			Name:     "block-write-with-subquery",
			Action:   ActionDeny,
			Patterns: []string{`(?is)\b(?:update|delete)\b[^;]*\bwhere\b[^;]*\(\s*select\b`},
		}})
		require.NoError(t, err)

		got := rs.Evaluate("UPDATE t SET x=1 WHERE id IN (SELECT id FROM u)", KindSQL, Attrs{})
		assert.True(t, got.Denied)

		got = rs.Evaluate("UPDATE t SET x=1 WHERE id = 5", KindSQL, Attrs{})
		assert.False(t, got.Denied)
	})

	t.Run("block-cte-write", func(t *testing.T) {
		t.Parallel()

		rs, err := NewRuleset([]Rule{{
			Name:     "block-cte-write",
			Action:   ActionDeny,
			Patterns: []string{`(?is)(?:^|;)\s*with\b[^;]*\b(?:update|delete|insert|merge)\b`},
		}})
		require.NoError(t, err)

		got := rs.Evaluate("WITH c AS (SELECT 1) DELETE FROM t WHERE id IN (SELECT id FROM c)", KindSQL, Attrs{})
		assert.True(t, got.Denied)

		got = rs.Evaluate("WITH c AS (SELECT 1) SELECT * FROM c", KindSQL, Attrs{})
		assert.False(t, got.Denied)
	})

	t.Run("block-excessive-joins", func(t *testing.T) {
		t.Parallel()

		rs, err := NewRuleset([]Rule{{
			Name:     "block-excessive-joins",
			Action:   ActionWarn,
			Patterns: []string{`(?is)\bjoin\b(?:[^;]*\bjoin\b){2}`},
			Message:  "query has excessive joins",
		}})
		require.NoError(t, err)

		got := rs.Evaluate("SELECT * FROM a JOIN b ON a.id=b.id JOIN c ON b.id=c.id JOIN d ON c.id=d.id", KindSQL, Attrs{})
		assert.False(t, got.Denied)
		require.NotEmpty(t, got.Warnings)
		assert.Equal(t, "query has excessive joins", got.Warnings[0])

		got = rs.Evaluate("SELECT * FROM a JOIN b ON a.id=b.id", KindSQL, Attrs{})
		assert.Empty(t, got.Warnings)
	})

	t.Run("block-write-without-where", func(t *testing.T) {
		t.Parallel()

		rs, err := NewRuleset([]Rule{{
			Name:     "block-write-without-where",
			Action:   ActionDeny,
			Patterns: []string{`(?is)\b(?:update|delete)\b`},
			Absent:   []string{`(?is)\bwhere\b`},
		}})
		require.NoError(t, err)

		got := rs.Evaluate("UPDATE t SET x=1", KindSQL, Attrs{})
		assert.True(t, got.Denied)

		got = rs.Evaluate("UPDATE t SET x=1 WHERE id=2", KindSQL, Attrs{})
		assert.False(t, got.Denied)
	})
}
