package metrics

import "testing"

func TestObserverEnabled_nilInterface(t *testing.T) {
	t.Parallel()
	if ObserverEnabled(nil) {
		t.Fatal("expected false")
	}
}

func TestObserverEnabled_typedNilRegistry(t *testing.T) {
	t.Parallel()
	var reg *Registry
	var obs Observer = reg
	if ObserverEnabled(obs) {
		t.Fatal("expected false for typed nil *Registry in Observer")
	}
}

func TestObserverEnabled_registry(t *testing.T) {
	t.Parallel()
	reg := NewRegistry("test", "abc")
	var obs Observer = reg
	if !ObserverEnabled(obs) {
		t.Fatal("expected true")
	}
}
