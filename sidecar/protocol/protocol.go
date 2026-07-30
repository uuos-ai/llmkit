// Package protocol defines llmkit-sidecar v1 framing and control messages.
package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	Version                     = "1.0"
	DefaultMaxFrameBytes uint32 = 8 << 20
)

type Method string

const (
	MethodHandshake          Method = "handshake"
	MethodHealth             Method = "health"
	MethodShutdown           Method = "shutdown"
	MethodCancel             Method = "cancel"
	MethodEnroll             Method = "enroll_client"
	MethodListCapabilities   Method = "list_capabilities"
	MethodListModels         Method = "list_models"
	MethodValidateCredential Method = "validate_credential"
	MethodAvailableTargets   Method = "get_available_targets"
	// MethodResolveOptions is retained as a v1 compatibility alias. New SDKs
	// use MethodAvailableTargets and the "available target list" terminology.
	MethodResolveOptions Method = "resolve_provider_options"
	MethodUpsertCustom   Method = "upsert_custom_provider"
	MethodDeleteCustom   Method = "delete_custom_provider"
	MethodGenerate       Method = "generate"
	MethodEmbed          Method = "embed"
	MethodRerank         Method = "rerank"
	MethodModerate       Method = "moderate"
)

type MessageType string

const (
	TypeResult MessageType = "result"
	TypeEvent  MessageType = "event"
	TypeError  MessageType = "error"
	TypeEnd    MessageType = "end"
)

type Request struct {
	Version    string          `json:"version"`
	SessionKey string          `json:"session_key"`
	RequestID  string          `json:"request_id"`
	Method     Method          `json:"method"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

type Response struct {
	Version   string          `json:"version"`
	RequestID string          `json:"request_id"`
	Type      MessageType     `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Error     *Error          `json:"error,omitempty"`
}

type Error struct {
	Kind         string `json:"kind"`
	Message      string `json:"message"`
	Retryable    bool   `json:"retryable,omitempty"`
	RetryAfterMS int64  `json:"retry_after_ms,omitempty"`
	StatusCode   int    `json:"status_code,omitempty"`
	ProviderCode string `json:"provider_code,omitempty"`
	RequestID    string `json:"provider_request_id,omitempty"`
}

type HandshakeRequest struct {
	SupportedVersions []string `json:"supported_versions"`
	HostBuildID       string   `json:"host_build_id,omitempty"`
}

type HandshakeResponse struct {
	ProtocolVersion  string   `json:"protocol_version"`
	SidecarVersion   string   `json:"sidecar_version"`
	LLMKitVersion    string   `json:"llmkit_version"`
	BuildID          string   `json:"build_id"`
	InstanceID       string   `json:"instance_id"`
	ClientID         string   `json:"client_id"`
	ClientInstanceID string   `json:"client_instance_id,omitempty"`
	UserID           string   `json:"user_id,omitempty"`
	BindingVersion   uint64   `json:"binding_version,omitempty"`
	Methods          []Method `json:"methods"`
}

type HealthResponse struct {
	Status string `json:"status"`
}

type CancelRequest struct {
	RequestID string `json:"request_id"`
}

type Codec struct {
	reader   io.Reader
	writer   io.Writer
	maxFrame uint32
}

func NewCodec(reader io.Reader, writer io.Writer, maxFrame uint32) *Codec {
	if maxFrame == 0 {
		maxFrame = DefaultMaxFrameBytes
	}
	return &Codec{reader: reader, writer: writer, maxFrame: maxFrame}
}

func (c *Codec) ReadRequest() (Request, error) {
	var request Request
	if err := c.readJSON(&request); err != nil {
		return Request{}, err
	}
	return request, nil
}

func (c *Codec) ReadResponse() (Response, error) {
	var response Response
	if err := c.readJSON(&response); err != nil {
		return Response{}, err
	}
	return response, nil
}

func (c *Codec) WriteRequest(request Request) error    { return c.writeJSON(request) }
func (c *Codec) WriteResponse(response Response) error { return c.writeJSON(response) }

func (c *Codec) readJSON(destination any) error {
	var header [4]byte
	if _, err := io.ReadFull(c.reader, header[:]); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > c.maxFrame {
		return fmt.Errorf("sidecar protocol: invalid frame length %d", length)
	}
	body := make([]byte, length)
	defer clear(body)
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return fmt.Errorf("sidecar protocol: truncated frame: %w", err)
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return errors.New("sidecar protocol: malformed JSON")
	}
	return nil
}

func (c *Codec) writeJSON(value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return errors.New("sidecar protocol: response encoding failed")
	}
	defer clear(body)
	if len(body) == 0 || uint64(len(body)) > uint64(c.maxFrame) {
		return fmt.Errorf("sidecar protocol: encoded frame exceeds %d bytes", c.maxFrame)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	if err := writeAll(c.writer, header[:]); err != nil {
		return err
	}
	return writeAll(c.writer, body)
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}
