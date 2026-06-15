// Command klauscode is a small ReAct AI harness. It drives an OpenAI model through
// a Thought/Action/Observation loop, executing tool calls (e.g. calculate) on
// the model's behalf until it produces a final answer.
//
// Usage:
//
//	export OPENAI_API_KEY=sk-...
//	go run ./cmd/klauscode "What is (12 * 9) + 3?"
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"klauscode/internal/agent"
	"klauscode/internal/llm"
	"klauscode/internal/tools"
	"klauscode/internal/tools/calculate"
)

// defaultModel is used when OPENAI_MODEL is not set.
const defaultModel = "gpt-4o-mini"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("OPENAI_API_KEY is not set")
	}

	task := strings.TrimSpace(strings.Join(os.Args[1:], " "))
	if task == "" {
		return fmt.Errorf("usage: klauscode \"<your question>\"")
	}

	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = defaultModel
	}

	// Composition root: wire concrete implementations together.
	client := llm.NewOpenAIClient(apiKey, model)

	registry := tools.NewRegistry()
	registry.Register(calculate.New())

	ag := agent.New(client, registry, agent.WithTrace(os.Stderr))

	answer, err := ag.Run(context.Background(), task)
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "--- final answer ---")
	fmt.Println(answer)
	return nil
}
