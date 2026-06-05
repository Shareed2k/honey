package cuetry

import "testing"

func TestEvalResultBoolExpr(t *testing.T) {
	tests := []struct {
		name string
		expr string
		ctx  ResultExprContext
		want bool
	}{
		{
			name: "exit code failure",
			expr: "exit_code != 0",
			ctx:  ResultExprContext{ExitCode: 2, Succeeded: false},
			want: true,
		},
		{
			name: "stdout contains",
			expr: `stdout.contains("changed")`,
			ctx:  ResultExprContext{Stdout: "service changed", Succeeded: true},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EvalResultBoolExpr(tt.expr, tt.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
