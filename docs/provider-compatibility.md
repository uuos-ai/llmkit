# Provider compatibility matrix

Status values:

- **Tested**: covered by offline protocol fixtures and the conformance harness.
- **Not applicable**: the protocol does not expose the capability.
- **Not declared**: this adapter intentionally does not advertise the capability.

| Provider protocol | Generate | Stream | Embed | Tools | Structured output | Reasoning | Usage | Error mapping |
|---|---|---|---|---|---|---|---|---|
| OpenAI Chat Completions | Tested | Tested | Tested | Tested | Tested | Tested | Tested | Tested |
| OpenAI Responses | Tested | Tested | Tested | Tested | Tested | Tested | Tested | Tested |
| Anthropic Messages | Tested | Tested | Not applicable | Tested | Tested | Tested | Tested | Tested |
| Google Gemini generateContent | Tested | Tested | Tested | Tested | Tested | Tested | Tested | Tested |
| DeepSeek OpenAI-compatible Chat | Tested | Tested | Not applicable | Tested | Tested | Tested | Tested | Tested |
| Alibaba DashScope/Qwen OpenAI-compatible | Tested | Tested | Tested | Tested | Tested | Tested | Tested | Tested |
| Volcengine Ark/Doubao Responses | Tested | Tested | Not applicable | Tested | Tested | Tested | Tested | Tested |
| Zhipu GLM Chat | Tested | Tested | Not applicable | Tested | Not declared | Tested | Tested | Tested |
| Moonshot/Kimi Chat | Tested | Tested | Not applicable | Tested | Tested | Tested | Tested | Tested |
| MiniMax Chat | Tested | Tested | Not applicable | Tested | Not declared | Tested | Tested | Tested |
| Tencent Hunyuan Chat/Embedding | Tested | Tested | Tested | Tested | Not declared | Not declared | Tested | Tested |
| Tencent TokenHub Chat | Tested | Tested | Not applicable | Not declared | Not declared | Not declared | Tested | Tested |
| Baidu Qianfan v2 | Tested | Tested | Tested | Tested | Tested | Tested | Tested | Tested |
| SiliconFlow Chat/Embedding/Rerank | Tested | Tested | Tested | Tested | Tested | Tested | Tested | Tested |
| Azure OpenAI v1 | Tested | Tested | Tested | Tested | Tested | Tested | Tested | Tested |
| Amazon Bedrock Mantle | Tested | Tested | Not applicable | Tested | Tested | Tested | Tested | Tested |
| Vertex AI OpenAI-compatible | Tested | Tested | Not applicable | Tested | Tested | Tested | Tested | Tested |
| OpenAI Moderation | Not applicable | Not applicable | Not applicable | Not applicable | Not applicable | Not applicable | Not applicable | Tested |

All listed providers implement request-scoped credential validation and remote
model enumeration. OpenAI-compatible profiles use their dedicated compatible
model endpoint; Anthropic and Gemini use their native paginated model APIs.

Every listed OpenAI-compatible profile runs an independent offline generation,
streaming, usage, tool-call, structured-output, reasoning, embedding,
rate-limit, secret-redaction, and malformed-response contract whenever it
declares that capability. OpenAI, Anthropic, and Gemini additionally run native
rich-response round trips for tools, structured output, and reasoning.

Every built-in adapter exposes a validated `AdapterManifest` with provider API
version, operations, capabilities, auth schemes, profile, and maturity. P0
adapters are `conformant`; `verified` is reserved for a dated live-provider
verification record and is never inferred from offline fixtures.

Azure, Bedrock, and Vertex profiles require an explicit authorized target
endpoint. They do not fall back to an executable placeholder. The profiles
follow the vendors' OpenAI-compatible surfaces: Azure OpenAI v1, Bedrock
Mantle, and Vertex AI Chat Completions. Workload credentials remain
request-scoped `CredentialHandle` implementations owned by the host.

## OpenAI protocol notes

- Both Chat Completions and Responses are native paths; select them explicitly
  with the provider option `openai.api`.
- Chat streams require a finish reason and `[DONE]`.
- Responses streams require a terminal `response.completed` or
  `response.incomplete` event.
- Unknown provider options are rejected.
- Error bodies are bounded and never included in public error strings.
- The tests use independently authored offline fixtures and require no API key.

## Anthropic protocol notes

- The adapter targets the native Messages endpoint and API version
  `2023-06-01`.
- `system` content is moved to the top-level system field, and llmkit tool
  result messages are represented as Anthropic `user` turns containing
  `tool_result` blocks.
- Streams require both a finish-bearing `message_delta` and the terminal
  `message_stop`; an early EOF is a malformed response.
- Tool input fragments are emitted as normalized tool-argument deltas without
  attempting to parse incomplete JSON.
- JSON outputs use `output_config.format`; cache read/write counters are
  preserved in normalized usage.
- Unknown stream event and delta variants are ignored for forward
  compatibility.
- Protocol references (retrieved 2026-07-28):
  [Messages API](https://platform.claude.com/docs/en/api/messages/create),
  [streaming Messages](https://platform.claude.com/docs/en/build-with-claude/streaming),
  and [structured outputs](https://platform.claude.com/docs/en/build-with-claude/structured-outputs).

## Google Gemini protocol notes

- The adapter targets native `generateContent`, `streamGenerateContent`, and
  `batchEmbedContents` methods under `v1beta`.
- Request-scoped credentials are expected to set the `x-goog-api-key` header;
  API keys are never stored in provider configuration.
- llmkit assistant turns map to Gemini `model` content, while tool result turns
  map to `function` content. Tool results therefore retain both call ID and
  function name in the normalized type.
- SSE completion requires a candidate finish reason; an early EOF is treated as
  a malformed response.
- Prompt, candidate, cached, thought, and total token counters are normalized
  from `usageMetadata`.
- Protocol references (retrieved 2026-07-28):
  [generateContent](https://ai.google.dev/api/generate-content),
  [embeddings](https://ai.google.dev/api/embeddings), and
  [structured outputs](https://ai.google.dev/gemini-api/docs/structured-output).

## Domestic OpenAI-compatible protocol notes

- Compatibility is expressed through dedicated Provider packages rather than
  treating every service as an undifferentiated OpenAI endpoint. Each package
  controls its endpoint layout and advertised capabilities.
- DeepSeek targets the documented `/chat/completions` path and deliberately
  does not implement `Embedder`.
- DashScope accepts region or workspace-specific compatible base URLs and
  supports Chat, Responses, and text embeddings through the shared codec.
- Protocol references (retrieved 2026-07-29):
  [DeepSeek API](https://api-docs.deepseek.com/guides/function_calling/),
  [DashScope base URLs](https://help.aliyun.com/en/model-studio/base-url),
  [Qwen OpenAI compatibility](https://help.aliyun.com/en/model-studio/compatibility-of-openai-with-dashscope),
  and [DashScope embeddings](https://help.aliyun.com/en/model-studio/embedding-interfaces-compatible-with-openai).

- Additional profile references (retrieved 2026-07-29):
  [MiniMax Chat](https://platform.minimaxi.com/docs/api-reference/text-chat-openai),
  [Zhipu OpenAI compatibility](https://docs.bigmodel.cn/cn/guide/develop/openai/introduction),
  [Volcengine Ark Responses](https://www.volcengine.com/docs/82379/1795150), and
  [Tencent Hunyuan compatibility](https://cloud.tencent.com/document/product/1729/111007).
- Moonshot/Kimi reference (retrieved 2026-07-29):
  [Kimi Chat API](https://platform.kimi.ai/docs/api/chat).
