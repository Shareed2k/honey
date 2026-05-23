package postgres

import "testing"

func TestValidateReadonlySQL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		sql   string
		valid bool
	}{
		{"SELECT 1", true},
		{"WITH x AS (SELECT 1) SELECT * FROM x", true},
		{"INSERT INTO t VALUES (1)", false},
		{"SELECT 1; SELECT 2", false},
	}
	for _, tc := range cases {
		err := ValidateReadonlySQL(tc.sql)
		if tc.valid && err != nil {
			t.Fatalf("sql=%q expected valid: %v", tc.sql, err)
		}
		if !tc.valid && err == nil {
			t.Fatalf("sql=%q expected invalid", tc.sql)
		}
	}
}

func TestValidateParamPlaceholders(t *testing.T) {
	t.Parallel()
	if err := ValidateParamPlaceholders("SELECT $1, $2", 2); err != nil {
		t.Fatal(err)
	}
	if err := ValidateParamPlaceholders("SELECT $1", 2); err == nil {
		t.Fatal("expected missing placeholder error")
	}
}
