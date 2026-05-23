package postgres

import (
	"fmt"
	"strings"
	"unicode"
)

var readonlyVerbs = map[string]struct{}{
	"SELECT": {}, "WITH": {}, "SHOW": {}, "EXPLAIN": {}, "TABLE": {},
}

// ValidateReadonlySQL returns nil when sql is a single read-only statement.
func ValidateReadonlySQL(sql string) error {
	stmt, err := singleStatement(trimSQLComments(sql))
	if err != nil {
		return err
	}
	word, err := firstKeyword(stmt)
	if err != nil {
		return err
	}
	if _, ok := readonlyVerbs[word]; !ok {
		return fmt.Errorf("postgres: sql is not read-only (first keyword %q)", word)
	}
	return nil
}

// IsReadonlySQL reports whether sql passes the read-only classifier.
func IsReadonlySQL(sql string) bool {
	return ValidateReadonlySQL(sql) == nil
}

func singleStatement(sql string) (string, error) {
	sql = strings.TrimSpace(sql)
	if sql == "" {
		return "", fmt.Errorf("postgres: sql is empty")
	}
	parts := splitStatements(sql)
	if len(parts) > 1 {
		return "", fmt.Errorf("postgres: multiple SQL statements are not allowed")
	}
	return parts[0], nil
}

func splitStatements(sql string) []string {
	var out []string
	var b strings.Builder
	inSingle := false
	inDouble := false
	inLineComment := false
	inBlockComment := false
	for i := 0; i < len(sql); i++ {
		ch := sql[i]
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
				b.WriteByte(ch)
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < len(sql) && sql[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if !inSingle && !inDouble {
			if ch == '-' && i+1 < len(sql) && sql[i+1] == '-' {
				inLineComment = true
				i++
				continue
			}
			if ch == '/' && i+1 < len(sql) && sql[i+1] == '*' {
				inBlockComment = true
				i++
				continue
			}
		}
		if ch == '\'' && !inDouble {
			if inSingle && i+1 < len(sql) && sql[i+1] == '\'' {
				b.WriteByte(ch)
				b.WriteByte(sql[i+1])
				i++
				continue
			}
			inSingle = !inSingle
			b.WriteByte(ch)
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			b.WriteByte(ch)
			continue
		}
		if ch == ';' && !inSingle && !inDouble {
			if s := strings.TrimSpace(b.String()); s != "" {
				out = append(out, s)
			}
			b.Reset()
			continue
		}
		b.WriteByte(ch)
	}
	if s := strings.TrimSpace(b.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func trimSQLComments(sql string) string {
	return strings.TrimSpace(sql)
}

func firstKeyword(stmt string) (string, error) {
	stmt = strings.TrimSpace(stmt)
	if stmt == "" {
		return "", fmt.Errorf("postgres: sql is empty")
	}
	i := 0
	for i < len(stmt) && unicode.IsSpace(rune(stmt[i])) {
		i++
	}
	j := i
	for j < len(stmt) && !unicode.IsSpace(rune(stmt[j])) {
		j++
	}
	if j == i {
		return "", fmt.Errorf("postgres: missing SQL keyword")
	}
	return strings.ToUpper(stmt[i:j]), nil
}
