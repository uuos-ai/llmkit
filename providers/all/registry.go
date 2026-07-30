// Package all constructs the built-in llmkit provider registry.
package all

import (
	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/providers/anthropic"
	"github.com/uuos-ai/llmkit/providers/dashscope"
	"github.com/uuos-ai/llmkit/providers/deepseek"
	"github.com/uuos-ai/llmkit/providers/gemini"
	"github.com/uuos-ai/llmkit/providers/hunyuan"
	"github.com/uuos-ai/llmkit/providers/minimax"
	"github.com/uuos-ai/llmkit/providers/moonshot"
	"github.com/uuos-ai/llmkit/providers/openai"
	"github.com/uuos-ai/llmkit/providers/volcengine"
	"github.com/uuos-ai/llmkit/providers/zhipu"
)

func NewRegistry() (*llmkit.Registry, error) {
	registry := llmkit.NewRegistry()
	constructors := []func() (llmkit.Provider, error){
		func() (llmkit.Provider, error) { return openai.New(openai.Config{}) },
		func() (llmkit.Provider, error) { return anthropic.New(anthropic.Config{}) },
		func() (llmkit.Provider, error) { return gemini.New(gemini.Config{}) },
		func() (llmkit.Provider, error) { return deepseek.New(deepseek.Config{}) },
		func() (llmkit.Provider, error) { return dashscope.New(dashscope.Config{}) },
		func() (llmkit.Provider, error) { return minimax.New(minimax.Config{}) },
		func() (llmkit.Provider, error) { return zhipu.New(zhipu.Config{}) },
		func() (llmkit.Provider, error) { return volcengine.New(volcengine.Config{}) },
		func() (llmkit.Provider, error) { return hunyuan.New(hunyuan.Config{}) },
		func() (llmkit.Provider, error) { return moonshot.New(moonshot.Config{}) },
	}
	for _, construct := range constructors {
		provider, err := construct()
		if err != nil {
			return nil, err
		}
		if err := registry.Register(provider); err != nil {
			return nil, err
		}
	}
	return registry, nil
}
