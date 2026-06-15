package calculate

import (
	"math"
	"testing"
)

func TestEval(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want float64
	}{
		{"single number", "42", 42},
		{"addition", "2 + 3", 5},
		{"precedence", "2 + 3 * 4", 14},
		{"parentheses override precedence", "(2 + 3) * 4", 20},
		{"subtraction left assoc", "10 - 3 - 2", 5},
		{"division", "20 / 4", 5},
		{"unary minus", "-5 + 2", -3},
		{"unary minus on parens", "-(2 + 3)", -5},
		{"decimals", "1.5 * 2", 3},
		{"nested parens", "((1 + 2) * (3 + 4))", 21},
		{"the poc question", "(12 * 9) + 3", 111},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// given an arithmetic expression
			// when it is evaluated
			got, err := Eval(tc.expr)
			// then the result matches
			if err != nil {
				t.Fatalf("Eval(%q) returned error: %v", tc.expr, err)
			}
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("Eval(%q) = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestEvalErrors(t *testing.T) {
	tests := []struct {
		name string
		expr string
	}{
		{"division by zero", "1 / 0"},
		{"trailing input", "1 2"},
		{"empty", ""},
		{"unbalanced paren", "(1 + 2"},
		{"bad character", "2 $ 3"},
		{"dangling operator", "2 +"},
		{"leading bad character reports real cause", "$2 + 3"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// given a malformed expression
			// when it is evaluated
			_, err := Eval(tc.expr)
			// then an error is returned
			if err == nil {
				t.Errorf("Eval(%q) expected error, got nil", tc.expr)
			}
		})
	}
}
