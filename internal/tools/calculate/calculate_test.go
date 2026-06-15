package calculate

import "testing"

func TestCalculateToolCall(t *testing.T) {
	// given a calculate tool
	tool := New()

	// when called with a valid expression
	got, err := tool.Call("2 + 3 * 4")

	// then it returns the formatted result
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	if got != "14" {
		t.Errorf("Call(\"2 + 3 * 4\") = %q, want %q", got, "14")
	}
}

func TestCalculateToolCallError(t *testing.T) {
	// given a calculate tool
	tool := New()

	// when called with an invalid expression
	_, err := tool.Call("1 / 0")

	// then an error is returned
	if err == nil {
		t.Error("expected error for division by zero, got nil")
	}
}
