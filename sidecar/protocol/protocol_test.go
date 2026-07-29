package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCodecRoundTrip(t *testing.T) {
	var stream bytes.Buffer
	writer := NewCodec(nil, &stream, 1024)
	want := Request{Version: Version, SessionKey: "secret", RequestID: "r1", Method: MethodHealth}
	if err := writer.WriteRequest(want); err != nil {
		t.Fatal(err)
	}
	reader := NewCodec(&stream, nil, 1024)
	got, err := reader.ReadRequest()
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != want.Version || got.SessionKey != want.SessionKey || got.RequestID != want.RequestID {
		t.Fatalf("request = %#v", got)
	}
}

func TestCodecRejectsOversizedAndTruncatedFrames(t *testing.T) {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], 1025)
	_, err := NewCodec(bytes.NewReader(header[:]), nil, 1024).ReadRequest()
	if err == nil || !strings.Contains(err.Error(), "invalid frame length") {
		t.Fatalf("error = %v", err)
	}

	binary.BigEndian.PutUint32(header[:], 10)
	stream := append(header[:], []byte("{}")...)
	_, err = NewCodec(bytes.NewReader(stream), nil, 1024).ReadRequest()
	if err == nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error = %v", err)
	}
}

func FuzzCodecReadRequest(f *testing.F) {
	f.Add([]byte{0, 0, 0, 2, '{', '}'})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = NewCodec(bytes.NewReader(data), nil, 4096).ReadRequest()
	})
}

func BenchmarkCodecRoundTrip(b *testing.B) {
	request := Request{
		Version: Version, SessionKey: strings.Repeat("x", 32),
		RequestID: "benchmark", Method: MethodHealth,
	}
	b.ReportAllocs()
	for range b.N {
		var stream bytes.Buffer
		if err := NewCodec(nil, &stream, 4096).WriteRequest(request); err != nil {
			b.Fatal(err)
		}
		if _, err := NewCodec(&stream, nil, 4096).ReadRequest(); err != nil {
			b.Fatal(err)
		}
	}
}
