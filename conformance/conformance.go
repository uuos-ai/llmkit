// Package conformance provides reusable behavioral contracts for Provider
// adapters. Provider packages supply protocol-specific fake upstream fixtures.
package conformance

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/uuos-ai/llmkit"
)

type GenerationCase struct {
	Name         string
	Generator    llmkit.Generator
	Call         llmkit.GenerateCall
	ExpectedText string
	FinishReason llmkit.FinishReason
	UsageSource  llmkit.UsageSource
}

func RunGeneration(t *testing.T, testCase GenerationCase) {
	t.Helper()
	t.Run(testCase.Name, func(t *testing.T) {
		response, err := testCase.Generator.Generate(context.Background(), testCase.Call)
		if err != nil {
			t.Fatal(err)
		}
		if got := textContent(response.Message); got != testCase.ExpectedText {
			t.Fatalf("text = %q, want %q", got, testCase.ExpectedText)
		}
		if response.FinishReason != testCase.FinishReason {
			t.Fatalf("finish reason = %q, want %q", response.FinishReason, testCase.FinishReason)
		}
		if response.Usage.Source != testCase.UsageSource {
			t.Fatalf("usage source = %q, want %q", response.Usage.Source, testCase.UsageSource)
		}
	})
}

type StreamingCase struct {
	Name          string
	Generator     llmkit.StreamGenerator
	Call          llmkit.GenerateCall
	ExpectedText  string
	FinishReason  llmkit.FinishReason
	RequireUsage  bool
	RequireFinish bool
}

func RunStreaming(t *testing.T, testCase StreamingCase) {
	t.Helper()
	t.Run(testCase.Name, func(t *testing.T) {
		stream, err := testCase.Generator.Stream(context.Background(), testCase.Call)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()

		var text string
		var finish llmkit.FinishReason
		var sawUsage bool
		var sawFinish bool
		for {
			event, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			switch event.Type {
			case llmkit.EventTextDelta:
				text += event.Text
			case llmkit.EventUsage:
				sawUsage = event.Usage != nil
			case llmkit.EventFinish:
				sawFinish = true
				finish = event.FinishReason
			}
		}
		if text != testCase.ExpectedText {
			t.Fatalf("text = %q, want %q", text, testCase.ExpectedText)
		}
		if testCase.RequireUsage && !sawUsage {
			t.Fatal("stream did not emit usage")
		}
		if testCase.RequireFinish && !sawFinish {
			t.Fatal("stream did not emit finish")
		}
		if sawFinish && finish != testCase.FinishReason {
			t.Fatalf("finish reason = %q, want %q", finish, testCase.FinishReason)
		}
	})
}

type ErrorCase struct {
	Name       string
	Invoke     func(context.Context) error
	Kind       llmkit.ErrorKind
	Retryable  bool
	StatusCode int
}

func RunError(t *testing.T, testCase ErrorCase) {
	t.Helper()
	t.Run(testCase.Name, func(t *testing.T) {
		err := testCase.Invoke(context.Background())
		var providerErr *llmkit.ProviderError
		if !errors.As(err, &providerErr) {
			t.Fatalf("error = %T, want *llmkit.ProviderError", err)
		}
		if providerErr.Kind != testCase.Kind ||
			providerErr.Retryable != testCase.Retryable ||
			providerErr.StatusCode != testCase.StatusCode {
			t.Fatalf("unexpected provider error: %#v", providerErr)
		}
	})
}

type RerankCase struct {
	Name          string
	Reranker      llmkit.Reranker
	Call          llmkit.RerankCall
	ExpectedCount int
	UsageSource   llmkit.UsageSource
}

func RunRerank(t *testing.T, testCase RerankCase) {
	t.Helper()
	t.Run(testCase.Name, func(t *testing.T) {
		response, err := testCase.Reranker.Rerank(context.Background(), testCase.Call)
		if err != nil {
			t.Fatal(err)
		}
		if len(response.Results) != testCase.ExpectedCount || response.Usage.Source != testCase.UsageSource {
			t.Fatalf("unexpected rerank response: %#v", response)
		}
		for _, result := range response.Results {
			if result.Index < 0 || result.Index >= len(testCase.Call.Documents) {
				t.Fatalf("invalid result index: %#v", result)
			}
		}
	})
}

type ModerationCase struct {
	Name        string
	Moderator   llmkit.Moderator
	Call        llmkit.ModerateCall
	Flagged     bool
	MinCategory int
	UsageSource llmkit.UsageSource
}

func RunModeration(t *testing.T, testCase ModerationCase) {
	t.Helper()
	t.Run(testCase.Name, func(t *testing.T) {
		response, err := testCase.Moderator.Moderate(context.Background(), testCase.Call)
		if err != nil {
			t.Fatal(err)
		}
		if response.Flagged != testCase.Flagged || len(response.Categories) < testCase.MinCategory || response.Usage.Source != testCase.UsageSource {
			t.Fatalf("unexpected moderation response: %#v", response)
		}
	})
}

func textContent(message llmkit.Message) string {
	var result string
	for _, part := range message.Parts {
		if part.Type == llmkit.ContentText {
			result += part.Text
		}
	}
	return result
}
