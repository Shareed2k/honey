package ui

import (
	"testing"
)

func TestRecipeKVCoordinator_EnsureSession(t *testing.T) {
	c := NewRecipeKVCoordinator(0)
	defer c.Close()

	s1, err := c.EnsureSession()
	if err != nil {
		t.Fatal(err)
	}
	s2, err := c.EnsureSession()
	if err != nil {
		t.Fatal(err)
	}
	if s1 != s2 {
		t.Fatal("expected same session instance")
	}
	if err := s1.Put("x", "y"); err != nil {
		t.Fatal(err)
	}
	val, found, err := s2.Get("x")
	if err != nil || !found || val != "y" {
		t.Fatalf("get x: val=%q found=%v err=%v", val, found, err)
	}
}
