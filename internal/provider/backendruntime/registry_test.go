package backendruntime

import "testing"

type fakeRuntime struct {
	Name string
	URL  string
}

func TestRegistry_ByName_EmptyNameReturnsFirstEntry(t *testing.T) {
	t.Parallel()
	reg := New(func(r fakeRuntime) string { return r.Name })
	reg.Reconfigure([]fakeRuntime{{Name: "a", URL: "u1"}, {Name: "b", URL: "u2"}})

	got, ok := reg.ByName("")
	if !ok || got.URL != "u1" {
		t.Fatalf("ByName(\"\") = %+v, %v; want u1, true", got, ok)
	}
}

func TestRegistry_ByName_MatchesByName(t *testing.T) {
	t.Parallel()
	reg := New(func(r fakeRuntime) string { return r.Name })
	reg.Reconfigure([]fakeRuntime{{Name: "a", URL: "u1"}, {Name: "b", URL: "u2"}})

	got, ok := reg.ByName("b")
	if !ok || got.URL != "u2" {
		t.Fatalf("ByName(\"b\") = %+v, %v; want u2, true", got, ok)
	}
}

func TestRegistry_ByName_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	reg := New(func(r fakeRuntime) string { return r.Name })
	reg.Reconfigure([]fakeRuntime{{Name: "a", URL: "u1"}})

	got, ok := reg.ByName("  a  ")
	if !ok || got.URL != "u1" {
		t.Fatalf("ByName(\"  a  \") = %+v, %v; want u1, true", got, ok)
	}
}

func TestRegistry_ByName_NoMatchReturnsFalse(t *testing.T) {
	t.Parallel()
	reg := New(func(r fakeRuntime) string { return r.Name })
	reg.Reconfigure([]fakeRuntime{{Name: "a", URL: "u1"}})

	got, ok := reg.ByName("missing")
	if ok {
		t.Fatalf("ByName(\"missing\") = %+v, true; want zero value, false", got)
	}
	var zero fakeRuntime
	if got != zero {
		t.Fatalf("ByName(\"missing\") returned non-zero value %+v", got)
	}
}

func TestRegistry_ByName_EmptyRegistryReturnsFalse(t *testing.T) {
	t.Parallel()
	reg := New(func(r fakeRuntime) string { return r.Name })

	_, ok := reg.ByName("")
	if ok {
		t.Fatalf("ByName(\"\") on empty registry = true; want false")
	}
}

func TestRegistry_Reconfigure_ReplacesPreviousContents(t *testing.T) {
	t.Parallel()
	reg := New(func(r fakeRuntime) string { return r.Name })
	reg.Reconfigure([]fakeRuntime{{Name: "a", URL: "u1"}})
	reg.Reconfigure([]fakeRuntime{{Name: "b", URL: "u2"}})

	if _, ok := reg.ByName("a"); ok {
		t.Fatalf("ByName(\"a\") found stale entry after Reconfigure replaced contents")
	}
	got, ok := reg.ByName("b")
	if !ok || got.URL != "u2" {
		t.Fatalf("ByName(\"b\") = %+v, %v; want u2, true", got, ok)
	}
}
