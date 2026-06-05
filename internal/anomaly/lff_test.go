package anomaly

import (
	"testing"
)

func TestLFFTransformation(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    `[Sun Dec 04 20:34:21 2005] [notice] jk2_init() Found child 2008 in scoreboard slot 6`,
			expected: `[a 0 0:0:0 0] [a] a0_a() a 0 a 0`,
		},
		{
			input:    `[Sun Dec 04 20:34:25 2005] [notice] workerEnv.init() ok /etc/httpd/conf/workers2.properties`,
			expected: `[a 0 0:0:0 0] [a] a.a() a /a/a/a/a0.a`,
		},
	}

	for _, tt := range tests {
		got := LFF(tt.input)
		if got != tt.expected {
			t.Errorf("expected LFF %q, got %q", tt.expected, got)
		}
	}
}
