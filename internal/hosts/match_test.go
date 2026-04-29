package hosts

import "testing"

func TestNameMatchesSubstring(t *testing.T) {
	q := Query{NameSubstring: "foo"}
	ok, err := NameMatches("MyFooBar", q)
	if err != nil || !ok {
		t.Fatalf("expected match, ok=%v err=%v", ok, err)
	}
}

func TestNameMatchesRegex(t *testing.T) {
	q := Query{NameRegex: `^prod-`}
	ok, err := NameMatches("prod-web-1", q)
	if err != nil || !ok {
		t.Fatalf("expected match, ok=%v err=%v", ok, err)
	}
	q2 := Query{NameRegex: "["}
	_, err = NameMatches("x", q2)
	if err == nil {
		t.Fatal("expected invalid regex error")
	}
}
