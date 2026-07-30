// Package all constructs the built-in llmkit provider registry.
package all

import (
	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/providers/anthropic"
	"github.com/uuos-ai/llmkit/providers/azure"
	"github.com/uuos-ai/llmkit/providers/bedrock"
	"github.com/uuos-ai/llmkit/providers/dashscope"
	"github.com/uuos-ai/llmkit/providers/deepseek"
	"github.com/uuos-ai/llmkit/providers/gemini"
	"github.com/uuos-ai/llmkit/providers/hunyuan"
	"github.com/uuos-ai/llmkit/providers/minimax"
	"github.com/uuos-ai/llmkit/providers/moonshot"
	"github.com/uuos-ai/llmkit/providers/openai"
	"github.com/uuos-ai/llmkit/providers/qianfan"
	"github.com/uuos-ai/llmkit/providers/siliconflow"
	"github.com/uuos-ai/llmkit/providers/tokenhub"
	"github.com/uuos-ai/llmkit/providers/vertex"
	"github.com/uuos-ai/llmkit/providers/volcengine"
	"github.com/uuos-ai/llmkit/providers/zhipu"
	"github.com/uuos-ai/llmkit/transport"
)

func NewRegistry() (*llmkit.Registry, error) {
	return NewRegistryWithTransport(nil)
}

func NewRegistryWithTransport(client *transport.Client) (*llmkit.Registry, error) {
	registry := llmkit.NewRegistry()
	constructors := []func() (llmkit.Provider, error){
		func() (llmkit.Provider, error) { return openai.New(openai.Config{Transport: client}) },
		func() (llmkit.Provider, error) { return anthropic.New(anthropic.Config{Transport: client}) },
		func() (llmkit.Provider, error) { return gemini.New(gemini.Config{Transport: client}) },
		func() (llmkit.Provider, error) { return deepseek.New(deepseek.Config{Transport: client}) },
		func() (llmkit.Provider, error) { return dashscope.New(dashscope.Config{Transport: client}) },
		func() (llmkit.Provider, error) { return minimax.New(minimax.Config{Transport: client}) },
		func() (llmkit.Provider, error) { return zhipu.New(zhipu.Config{Transport: client}) },
		func() (llmkit.Provider, error) { return volcengine.New(volcengine.Config{Transport: client}) },
		func() (llmkit.Provider, error) { return hunyuan.New(hunyuan.Config{Transport: client}) },
		func() (llmkit.Provider, error) { return moonshot.New(moonshot.Config{Transport: client}) },
		func() (llmkit.Provider, error) { return tokenhub.New(tokenhub.Config{Transport: client}) },
		func() (llmkit.Provider, error) { return qianfan.New(qianfan.Config{Transport: client}) },
		func() (llmkit.Provider, error) { return siliconflow.New(siliconflow.Config{Transport: client}) },
		func() (llmkit.Provider, error) { return azure.New(azure.Config{Transport: client}) },
		func() (llmkit.Provider, error) { return bedrock.New(bedrock.Config{Transport: client}) },
		func() (llmkit.Provider, error) { return vertex.New(vertex.Config{Transport: client}) },
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
