package postgres

import "testing"

func TestRewriteDSNHostPort(t *testing.T) {
	out, err := RewriteDSNHostPort("postgres://user:pass@db.example.com:5432/app?sslmode=require", "127.0.0.1", "15432")
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(out, "127.0.0.1", "15432", "user:pass", "app") {
		t.Fatalf("got %q", out)
	}
	out, err = RewriteDSNHostPort("postgres://user:pass@db.example.com:5432/app", "127.0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(out, "127.0.0.1", "5432") {
		t.Fatalf("host only: got %q", out)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if p != "" && !stringContains(s, p) {
			return false
		}
	}
	return true
}

func stringContains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
