package config

import "testing"

func TestDefaultsMaxParallelValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want int
	}{
		{0, 0}, {-3, 0}, {1, 1}, {16, 16}, {128, 128}, {999, 128},
	}
	for _, c := range cases {
		if got := (Defaults{MaxParallel: c.in}).MaxParallelValue(); got != c.want {
			t.Errorf("MaxParallelValue(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestFileDefaultMaxParallel_nilSafe(t *testing.T) {
	t.Parallel()
	var nilF *File
	if got := nilF.DefaultMaxParallel(); got != 0 {
		t.Fatalf("nil *File: got %d, want 0", got)
	}
	f := &File{Defaults: Defaults{MaxParallel: 40}}
	if got := f.DefaultMaxParallel(); got != 40 {
		t.Fatalf("got %d, want 40", got)
	}
}
