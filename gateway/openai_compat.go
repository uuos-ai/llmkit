package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/identity"
)

type openAIChatRequest struct {
	Model               string              `json:"model"`
	Messages            []openAIMessage     `json:"messages"`
	Tools               []openAITool        `json:"tools,omitempty"`
	Stream              bool                `json:"stream,omitempty"`
	MaxTokens           *int64              `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int64              `json:"max_completion_tokens,omitempty"`
	Temperature         *float64            `json:"temperature,omitempty"`
	TopP                *float64            `json:"top_p,omitempty"`
	Stop                json.RawMessage     `json:"stop,omitempty"`
	ResponseFormat      *openAIResponseType `json:"response_format,omitempty"`
}

type openAIMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls,omitempty"`
}

type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type openAIResponseType struct {
	Type       string `json:"type"`
	JSONSchema *struct {
		Name   string          `json:"name"`
		Strict bool            `json:"strict,omitempty"`
		Schema json.RawMessage `json:"schema"`
	} `json:"json_schema,omitempty"`
}

func (s *Server) openAIChat(writer http.ResponseWriter, request *http.Request) {
	var input openAIChatRequest
	if !s.decodeOpenAI(writer, request, &input) {
		return
	}
	generate, err := input.normalize()
	if err != nil {
		writeOpenAIError(writer, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	principal, _ := identity.FromContext(request.Context())
	target, err := s.resolveTarget(request.Context(), principal, input.Model)
	if err != nil {
		writeOpenAIProviderError(writer, err)
		return
	}
	charge := s.reserveInference(writer, request, principal, target)
	if charge == nil {
		return
	}
	defer charge.finish(request.Context())
	credential, release, err := s.openCredential(request.Context(), principal, target.CredentialRef)
	if err != nil {
		writeOpenAIProviderError(writer, err)
		return
	}
	defer release()
	operationID := operationID(request)
	call := llmkit.GenerateCall{OperationID: operationID, Target: target.Target, Credential: credential, Request: generate}
	if input.Stream {
		s.openAIChatStream(writer, request, principal, target.ID, call, charge)
		return
	}
	generator, ok := s.config.Registry.Generator(target.Target.Provider)
	if !ok {
		writeOpenAIError(writer, http.StatusBadRequest, "invalid_request_error", "selected model does not support generation")
		return
	}
	response, err := generator.Generate(request.Context(), call)
	charge.observe(response.Usage)
	s.audit(request.Context(), principal, "openai.chat.completions", operationID, target.ID, response.Usage, err)
	if err != nil {
		writeOpenAIProviderError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, chatCompletion(target.ID, operationID, response))
}

func (input openAIChatRequest) normalize() (llmkit.GenerateRequest, error) {
	if input.Model == "" || len(input.Messages) == 0 {
		return llmkit.GenerateRequest{}, errors.New("model and messages are required")
	}
	request := llmkit.GenerateRequest{Temperature: input.Temperature, TopP: input.TopP}
	request.MaxOutputTokens = input.MaxCompletionTokens
	if request.MaxOutputTokens == nil {
		request.MaxOutputTokens = input.MaxTokens
	}
	stop, err := decodeStop(input.Stop)
	if err != nil {
		return llmkit.GenerateRequest{}, err
	}
	request.Stop = stop
	for _, message := range input.Messages {
		normalized, err := normalizeOpenAIMessage(message)
		if err != nil {
			return llmkit.GenerateRequest{}, err
		}
		request.Messages = append(request.Messages, normalized)
	}
	for _, tool := range input.Tools {
		if tool.Type != "function" || tool.Function.Name == "" || len(tool.Function.Parameters) == 0 {
			return llmkit.GenerateRequest{}, errors.New("only named function tools with parameters are supported")
		}
		request.Tools = append(request.Tools, llmkit.Tool{Name: tool.Function.Name, Description: tool.Function.Description, InputSchema: append([]byte(nil), tool.Function.Parameters...)})
	}
	if input.ResponseFormat != nil {
		switch input.ResponseFormat.Type {
		case "text":
		case "json_object":
			request.ResponseFormat = &llmkit.ResponseFormat{Name: "json_object"}
		case "json_schema":
			if input.ResponseFormat.JSONSchema == nil || input.ResponseFormat.JSONSchema.Name == "" || len(input.ResponseFormat.JSONSchema.Schema) == 0 {
				return llmkit.GenerateRequest{}, errors.New("response_format.json_schema is incomplete")
			}
			request.ResponseFormat = &llmkit.ResponseFormat{Name: input.ResponseFormat.JSONSchema.Name, Schema: append([]byte(nil), input.ResponseFormat.JSONSchema.Schema...), Strict: input.ResponseFormat.JSONSchema.Strict}
		default:
			return llmkit.GenerateRequest{}, errors.New("unsupported response_format type")
		}
	}
	return request, nil
}

func normalizeOpenAIMessage(input openAIMessage) (llmkit.Message, error) {
	role := llmkit.Role(input.Role)
	if role != llmkit.RoleSystem && role != llmkit.RoleUser && role != llmkit.RoleAssistant && role != llmkit.RoleTool {
		return llmkit.Message{}, errors.New("unsupported message role")
	}
	message := llmkit.Message{Role: role}
	if len(input.Content) > 0 && string(input.Content) != "null" {
		parts, err := normalizeOpenAIContent(input.Content)
		if err != nil {
			return llmkit.Message{}, err
		}
		message.Parts = append(message.Parts, parts...)
	}
	for _, tool := range input.ToolCalls {
		if tool.Type != "function" || tool.ID == "" || tool.Function.Name == "" {
			return llmkit.Message{}, errors.New("malformed function tool call")
		}
		message.Parts = append(message.Parts, llmkit.ContentPart{Type: llmkit.ContentToolCall, ToolCall: &llmkit.ToolCall{ID: tool.ID, Name: tool.Function.Name, Arguments: append([]byte(nil), tool.Function.Arguments...)}})
	}
	if role == llmkit.RoleTool {
		message.Parts = []llmkit.ContentPart{{Type: llmkit.ContentToolResult, ToolResult: &llmkit.ToolResult{CallID: input.ToolCallID, Name: input.Name, Content: message.Parts}}}
	}
	return message, nil
}

func normalizeOpenAIContent(raw json.RawMessage) ([]llmkit.ContentPart, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []llmkit.ContentPart{{Type: llmkit.ContentText, Text: text}}, nil
	}
	var content []struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		ImageURL *struct {
			URL string `json:"url"`
		} `json:"image_url,omitempty"`
	}
	if err := json.Unmarshal(raw, &content); err != nil {
		return nil, errors.New("message content must be a string or content array")
	}
	parts := make([]llmkit.ContentPart, 0, len(content))
	for _, part := range content {
		switch part.Type {
		case "text", "input_text":
			parts = append(parts, llmkit.ContentPart{Type: llmkit.ContentText, Text: part.Text})
		case "image_url":
			if part.ImageURL == nil || part.ImageURL.URL == "" || strings.HasPrefix(strings.ToLower(part.ImageURL.URL), "file:") {
				return nil, errors.New("image_url must be a non-file URL")
			}
			parts = append(parts, llmkit.ContentPart{Type: llmkit.ContentImage, Media: &llmkit.MediaContent{RemoteURL: part.ImageURL.URL}})
		default:
			return nil, errors.New("unsupported message content type")
		}
	}
	return parts, nil
}

func decodeStop(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, errors.New("stop must be a string or string array")
	}
	return many, nil
}

func chatCompletion(model, id string, response llmkit.Response) map[string]any {
	message := map[string]any{"role": "assistant", "content": responseText(response.Message)}
	if calls := responseToolCalls(response.Message); len(calls) != 0 {
		message["tool_calls"] = calls
	}
	return map[string]any{
		"id": id, "object": "chat.completion", "created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": response.FinishReason}},
		"usage":   openAIUsage(response.Usage),
	}
}

func (s *Server) openAIChatStream(writer http.ResponseWriter, request *http.Request, principal identity.Principal, model string, call llmkit.GenerateCall, charge *inferenceCharge) {
	generator, ok := s.config.Registry.StreamGenerator(call.Target.Provider)
	if !ok {
		writeOpenAIError(writer, http.StatusBadRequest, "invalid_request_error", "selected model does not support streaming")
		return
	}
	stream, err := generator.Stream(request.Context(), call)
	if err != nil {
		writeOpenAIProviderError(writer, err)
		return
	}
	defer stream.Close()
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeOpenAIError(writer, http.StatusInternalServerError, "server_error", "streaming is unavailable")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store")
	created := time.Now().Unix()
	writeOpenAIData(writer, map[string]any{"id": call.OperationID, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil}}})
	flusher.Flush()
	usage := llmkit.Usage{Source: llmkit.UsageMissing}
	for {
		event, receiveErr := stream.Recv()
		if receiveErr == io.EOF {
			receiveErr = truncatedStreamError(call.Target, "provider stream ended before a terminal event")
			writeOpenAIStreamError(writer, receiveErr)
			flusher.Flush()
			s.audit(request.Context(), principal, "openai.chat.completions.stream", call.OperationID, model, usage, receiveErr)
			return
		}
		if receiveErr != nil {
			writeOpenAIStreamError(writer, receiveErr)
			flusher.Flush()
			s.audit(request.Context(), principal, "openai.chat.completions.stream", call.OperationID, model, usage, receiveErr)
			return
		}
		if event.Usage != nil {
			usage = *event.Usage
			charge.observe(usage)
		}
		delta := map[string]any{}
		if event.Text != "" {
			delta["content"] = event.Text
		}
		if event.ToolCall != nil || len(event.ArgumentsDelta) != 0 {
			callDelta := map[string]any{"index": event.Index, "type": "function", "function": map[string]any{}}
			function := callDelta["function"].(map[string]any)
			if event.ToolCall != nil {
				callDelta["id"], function["name"] = event.ToolCall.ID, event.ToolCall.Name
			}
			if len(event.ArgumentsDelta) != 0 {
				function["arguments"] = string(event.ArgumentsDelta)
			}
			delta["tool_calls"] = []any{callDelta}
		}
		if event.Type == llmkit.EventResponseFailed || event.Type == llmkit.EventResponseCancelled {
			receiveErr = terminalStreamError(call.Target, event)
			writeOpenAIStreamError(writer, receiveErr)
			flusher.Flush()
			s.audit(request.Context(), principal, "openai.chat.completions.stream", call.OperationID, model, usage, receiveErr)
			return
		}
		terminal := event.Type == llmkit.EventFinish || event.Type == llmkit.EventResponseCompleted
		finish := any(nil)
		if terminal {
			finish = event.FinishReason
			if event.FinishReason == "" {
				finish = llmkit.FinishUnknown
			}
		}
		if len(delta) != 0 || finish != nil {
			writeOpenAIData(writer, map[string]any{"id": call.OperationID, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			flusher.Flush()
		}
		if terminal {
			writeOpenAIData(writer, "[DONE]")
			flusher.Flush()
			s.audit(request.Context(), principal, "openai.chat.completions.stream", call.OperationID, model, usage, nil)
			return
		}
	}
}

type openAIResponsesRequest struct {
	Model        string          `json:"model"`
	Input        json.RawMessage `json:"input"`
	Instructions string          `json:"instructions,omitempty"`
	Stream       bool            `json:"stream,omitempty"`
	Temperature  *float64        `json:"temperature,omitempty"`
	TopP         *float64        `json:"top_p,omitempty"`
	MaxTokens    *int64          `json:"max_output_tokens,omitempty"`
}

func (s *Server) openAIResponses(writer http.ResponseWriter, request *http.Request) {
	var input openAIResponsesRequest
	if !s.decodeOpenAI(writer, request, &input) {
		return
	}
	messages, err := normalizeResponsesInput(input.Input, input.Instructions)
	if err != nil || input.Model == "" {
		writeOpenAIError(writer, http.StatusBadRequest, "invalid_request_error", "model and valid input are required")
		return
	}
	principal, _ := identity.FromContext(request.Context())
	target, err := s.resolveTarget(request.Context(), principal, input.Model)
	if err != nil {
		writeOpenAIProviderError(writer, err)
		return
	}
	charge := s.reserveInference(writer, request, principal, target)
	if charge == nil {
		return
	}
	defer charge.finish(request.Context())
	credential, release, err := s.openCredential(request.Context(), principal, target.CredentialRef)
	if err != nil {
		writeOpenAIProviderError(writer, err)
		return
	}
	defer release()
	id := operationID(request)
	generateRequest := llmkit.GenerateRequest{Messages: messages, Temperature: input.Temperature, TopP: input.TopP, MaxOutputTokens: input.MaxTokens}
	if input.Stream {
		s.openAIResponsesStream(writer, request, principal, target.ID, llmkit.GenerateCall{OperationID: id, Target: target.Target, Credential: credential, Request: generateRequest}, charge)
		return
	}
	generator, ok := s.config.Registry.Generator(target.Target.Provider)
	if !ok {
		writeOpenAIError(writer, http.StatusBadRequest, "invalid_request_error", "selected model does not support generation")
		return
	}
	response, err := generator.Generate(request.Context(), llmkit.GenerateCall{OperationID: id, Target: target.Target, Credential: credential, Request: generateRequest})
	charge.observe(response.Usage)
	s.audit(request.Context(), principal, "openai.responses", id, target.ID, response.Usage, err)
	if err != nil {
		writeOpenAIProviderError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"id": id, "object": "response", "created_at": time.Now().Unix(), "status": "completed", "model": target.ID, "output": []any{map[string]any{"type": "message", "id": id + "-message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": responseText(response.Message), "annotations": []any{}}}}}, "usage": map[string]any{"input_tokens": response.Usage.InputTokens, "output_tokens": response.Usage.OutputTokens, "total_tokens": response.Usage.TotalTokens}})
}

func (s *Server) openAIResponsesStream(writer http.ResponseWriter, request *http.Request, principal identity.Principal, model string, call llmkit.GenerateCall, charge *inferenceCharge) {
	generator, ok := s.config.Registry.StreamGenerator(call.Target.Provider)
	if !ok {
		writeOpenAIError(writer, http.StatusBadRequest, "invalid_request_error", "selected model does not support streaming")
		return
	}
	stream, err := generator.Stream(request.Context(), call)
	if err != nil {
		writeOpenAIProviderError(writer, err)
		return
	}
	defer stream.Close()
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeOpenAIError(writer, http.StatusInternalServerError, "server_error", "streaming is unavailable")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store")
	sequence := uint64(0)
	writeEvent := func(eventType string, event map[string]any) {
		sequence++
		event["type"], event["sequence_number"] = eventType, sequence
		encoded, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", eventType, encoded)
		flusher.Flush()
	}
	responseObject := map[string]any{"id": call.OperationID, "object": "response", "status": "in_progress", "model": model, "output": []any{}}
	writeEvent("response.created", map[string]any{"response": responseObject})
	usage := llmkit.Usage{Source: llmkit.UsageMissing}
	for {
		event, receiveErr := stream.Recv()
		if receiveErr == io.EOF {
			receiveErr = truncatedStreamError(call.Target, "provider stream ended before a terminal event")
			normalized := normalizedError(receiveErr)
			writeEvent("response.failed", map[string]any{"response": map[string]any{"id": call.OperationID, "status": "failed", "error": map[string]any{"code": normalized.Kind, "message": normalized.Message}}})
			s.audit(request.Context(), principal, "openai.responses.stream", call.OperationID, model, usage, receiveErr)
			return
		}
		if receiveErr != nil {
			normalized := normalizedError(receiveErr)
			writeEvent("response.failed", map[string]any{"response": map[string]any{"id": call.OperationID, "status": "failed", "error": map[string]any{"code": normalized.Kind, "message": normalized.Message}}})
			s.audit(request.Context(), principal, "openai.responses.stream", call.OperationID, model, usage, receiveErr)
			return
		}
		if event.Usage != nil {
			usage = *event.Usage
			charge.observe(usage)
		}
		if event.Text != "" {
			writeEvent("response.output_text.delta", map[string]any{"item_id": call.OperationID + "-message", "output_index": event.Index, "content_index": 0, "delta": event.Text})
		}
		if len(event.ArgumentsDelta) != 0 {
			writeEvent("response.function_call_arguments.delta", map[string]any{"item_id": call.OperationID + "-tool", "output_index": event.Index, "delta": string(event.ArgumentsDelta)})
		}
		switch event.Type {
		case llmkit.EventFinish, llmkit.EventResponseCompleted:
			responseObject["status"] = "completed"
			responseObject["usage"] = map[string]any{"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens, "total_tokens": usage.TotalTokens}
			writeEvent("response.completed", map[string]any{"response": responseObject})
			writeOpenAIData(writer, "[DONE]")
			flusher.Flush()
			s.audit(request.Context(), principal, "openai.responses.stream", call.OperationID, model, usage, nil)
			return
		case llmkit.EventResponseFailed, llmkit.EventResponseCancelled:
			receiveErr = terminalStreamError(call.Target, event)
			normalized := normalizedError(receiveErr)
			writeEvent("response.failed", map[string]any{"response": map[string]any{"id": call.OperationID, "status": "failed", "error": map[string]any{"code": normalized.Kind, "message": normalized.Message}}})
			s.audit(request.Context(), principal, "openai.responses.stream", call.OperationID, model, usage, receiveErr)
			return
		}
	}
}

func truncatedStreamError(target llmkit.Target, message string) error {
	return &llmkit.ProviderError{Provider: target.Provider, Model: target.Model, Kind: llmkit.ErrorProtocol, Phase: llmkit.PhaseStream, SafeMessage: message}
}

func terminalStreamError(target llmkit.Target, event llmkit.StreamEvent) error {
	if event.Error != nil {
		return &llmkit.ProviderError{Provider: target.Provider, Model: target.Model, Kind: event.Error.Code, Phase: llmkit.PhaseStream, Retryable: event.Error.Retryable, RetryAfter: time.Duration(event.Error.RetryAfterMS) * time.Millisecond, SafeMessage: event.Error.Message}
	}
	if event.Type == llmkit.EventResponseCancelled {
		return &llmkit.ProviderError{Provider: target.Provider, Model: target.Model, Kind: llmkit.ErrorCancelled, Phase: llmkit.PhaseStream, SafeMessage: "provider stream was cancelled"}
	}
	return truncatedStreamError(target, "provider stream failed without normalized error details")
}

func normalizeResponsesInput(raw json.RawMessage, instructions string) ([]llmkit.Message, error) {
	messages := []llmkit.Message{}
	if instructions != "" {
		messages = append(messages, llmkit.Message{Role: llmkit.RoleSystem, Parts: []llmkit.ContentPart{{Type: llmkit.ContentText, Text: instructions}}})
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return append(messages, llmkit.Message{Role: llmkit.RoleUser, Parts: []llmkit.ContentPart{{Type: llmkit.ContentText, Text: text}}}), nil
	}
	var input []openAIMessage
	if err := json.Unmarshal(raw, &input); err != nil || len(input) == 0 {
		return nil, errors.New("invalid Responses input")
	}
	for _, item := range input {
		message, err := normalizeOpenAIMessage(item)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

type openAIEmbeddingRequest struct {
	Model      string          `json:"model"`
	Input      json.RawMessage `json:"input"`
	Dimensions *int            `json:"dimensions,omitempty"`
}

func (s *Server) openAIEmbeddings(writer http.ResponseWriter, request *http.Request) {
	var input openAIEmbeddingRequest
	if !s.decodeOpenAI(writer, request, &input) {
		return
	}
	values, err := decodeEmbeddingInput(input.Input)
	if err != nil || input.Model == "" {
		writeOpenAIError(writer, http.StatusBadRequest, "invalid_request_error", "model and string input are required")
		return
	}
	principal, _ := identity.FromContext(request.Context())
	target, err := s.resolveTarget(request.Context(), principal, input.Model)
	if err != nil {
		writeOpenAIProviderError(writer, err)
		return
	}
	charge := s.reserveInference(writer, request, principal, target)
	if charge == nil {
		return
	}
	defer charge.finish(request.Context())
	credential, release, err := s.openCredential(request.Context(), principal, target.CredentialRef)
	if err != nil {
		writeOpenAIProviderError(writer, err)
		return
	}
	defer release()
	embedder, ok := s.config.Registry.Embedder(target.Target.Provider)
	if !ok {
		writeOpenAIError(writer, http.StatusBadRequest, "invalid_request_error", "selected model does not support embeddings")
		return
	}
	id := operationID(request)
	response, err := embedder.Embed(request.Context(), llmkit.EmbedCall{OperationID: id, Target: target.Target, Credential: credential, Input: values, Dimensions: input.Dimensions})
	charge.observe(response.Usage)
	s.audit(request.Context(), principal, "openai.embeddings", id, target.ID, response.Usage, err)
	if err != nil {
		writeOpenAIProviderError(writer, err)
		return
	}
	data := make([]any, len(response.Vectors))
	for index, vector := range response.Vectors {
		data[index] = map[string]any{"object": "embedding", "index": index, "embedding": vector}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"object": "list", "data": data, "model": target.ID, "usage": openAIUsage(response.Usage)})
}

func decodeEmbeddingInput(raw json.RawMessage) ([]string, error) {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil || len(many) == 0 {
		return nil, errors.New("input must be a string or string array")
	}
	return many, nil
}

func (s *Server) decodeOpenAI(writer http.ResponseWriter, request *http.Request, destination any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxBodyBytes)
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(destination); err != nil {
		writeOpenAIError(writer, http.StatusBadRequest, "invalid_request_error", "request body is malformed")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeOpenAIError(writer, http.StatusBadRequest, "invalid_request_error", "exactly one JSON value is required")
		return false
	}
	return true
}

func responseText(message llmkit.Message) string {
	var value strings.Builder
	for _, part := range message.Parts {
		if part.Type == llmkit.ContentText {
			value.WriteString(part.Text)
		}
	}
	return value.String()
}

func responseToolCalls(message llmkit.Message) []any {
	var calls []any
	for _, part := range message.Parts {
		if part.Type == llmkit.ContentToolCall && part.ToolCall != nil {
			calls = append(calls, map[string]any{"id": part.ToolCall.ID, "type": "function", "function": map[string]any{"name": part.ToolCall.Name, "arguments": string(part.ToolCall.Arguments)}})
		}
	}
	return calls
}

func openAIUsage(usage llmkit.Usage) map[string]any {
	return map[string]any{"prompt_tokens": usage.InputTokens, "completion_tokens": usage.OutputTokens, "total_tokens": usage.TotalTokens}
}

func operationID(request *http.Request) string {
	if value := request.Header.Get("X-Request-ID"); value != "" {
		return value
	}
	return fmt.Sprintf("llmkit-%d", time.Now().UnixNano())
}

func writeOpenAIData(writer io.Writer, value any) {
	if text, ok := value.(string); ok {
		_, _ = fmt.Fprintf(writer, "data: %s\n\n", text)
		return
	}
	encoded, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(writer, "data: %s\n\n", encoded)
}

func writeOpenAIStreamError(writer io.Writer, err error) {
	normalized := normalizedError(err)
	writeOpenAIData(writer, map[string]any{"error": map[string]any{"message": normalized.Message, "type": normalized.Kind, "code": normalized.ProviderCode}})
}

func writeOpenAIProviderError(writer http.ResponseWriter, err error) {
	normalized := normalizedError(err)
	status := normalized.StatusCode
	if status < 400 || status > 599 {
		status = http.StatusBadGateway
	}
	writeOpenAIError(writer, status, normalized.Kind, normalized.Message)
}

func writeOpenAIError(writer http.ResponseWriter, status int, kind, message string) {
	writeJSON(writer, status, map[string]any{"error": map[string]any{"message": message, "type": kind, "param": nil, "code": nil}})
}
