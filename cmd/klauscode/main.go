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
	"klauscode/internal/tools/bash"
	"klauscode/internal/tools/calculate"
	"klauscode/internal/tools/editfile"
	"klauscode/internal/tools/readfile"
	"klauscode/internal/tools/webfetch"
	"klauscode/internal/tools/websearch"
	"klauscode/internal/tools/writefile"
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
	// OPENAI_BASE_URL points at an OpenAI-compatible server, e.g. a local
	// LM Studio at http://127.0.0.1:1234/v1. Empty means the public OpenAI API.
	baseURL := os.Getenv("OPENAI_BASE_URL")

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		if baseURL == "" {
			return fmt.Errorf("OPENAI_API_KEY is not set")
		}
		// Local OpenAI-compatible servers (e.g. LM Studio) ignore the key.
		apiKey = "not-needed"
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
	var opts []llm.Option
	if baseURL != "" {
		opts = append(opts, llm.WithBaseURL(baseURL))
	}
	client := llm.NewOpenAIClient(apiKey, model, opts...)

	registry := tools.NewRegistry()
	registry.Register(calculate.New())
	registry.Register(readfile.New())
	registry.Register(writefile.New())
	registry.Register(editfile.New())
	registry.Register(bash.New())
	registry.Register(websearch.New())
	registry.Register(webfetch.New())

	ag := agent.New(client, registry, agent.WithTrace(os.Stderr))

	answer, err := ag.Run(context.Background(), task)
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "--- final answer ---")
	fmt.Println(answer)
	return nil
}
