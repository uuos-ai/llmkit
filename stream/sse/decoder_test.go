package sse

import (
	"io"
	"strings"
	"testing"
	"time"
)

func FuzzDecoder(f *testing.F) {
	f.Add("data: hello\n\n")
	f.Add("event: message\r\ndata: {}\r\n\r\n")
	f.Fuzz(func(t *testing.T, input string) {
		decoder := NewDecoder(strings.NewReader(input), 4096)
		for index := 0; index < 1024; index++ {
			_, err := decoder.Next()
			if err != nil {
				return
			}
		}
		t.Fatal("decoder did not terminate within the event bound")
	})
}

func BenchmarkDecoder(b *testing.B) {
	input := strings.Repeat("data: token\n\n", 128)
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		decoder := NewDecoder(strings.NewReader(input), 4096)
		for {
			_, err := decoder.Next()
			if err != nil {
				break
			}
		}
	}
}

func TestDecoderParsesMultilineEvent(t *testing.T) {
	decoder := NewDecoder(strings.NewReader(
		": keepalive\n"+
			"id: 42\n"+
			"event: message\n"+
			"retry: 1500\n"+
			"data: first\n"+
			"data: second\n\n",
	), 0)
	event, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	if event.ID != "42" || event.Type != "message" || string(event.Data) != "first\nsecond" {
		t.Fatalf("unexpected event: %#v", event)
	}
	if event.Retry != 1500*time.Millisecond {
		t.Fatalf("retry = %v", event.Retry)
	}
	if _, err := decoder.Next(); err != io.EOF {
		t.Fatalf("terminal error = %v, want EOF", err)
	}
}

func TestDecoderDispatchesFinalEventAtEOF(t *testing.T) {
	decoder := NewDecoder(strings.NewReader("data: final"), 0)
	event, err := decoder.Next()
	if err != nil || string(event.Data) != "final" {
		t.Fatalf("event = %#v, error = %v", event, err)
	}
	if _, err := decoder.Next(); err != io.EOF {
		t.Fatalf("terminal error = %v, want EOF", err)
	}
}

func TestDecoderPersistsLastEventID(t *testing.T) {
	decoder := NewDecoder(strings.NewReader("id: first\ndata: one\n\ndata: two\n\n"), 0)
	first, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	second, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "first" || second.ID != "first" {
		t.Fatalf("IDs = %q, %q", first.ID, second.ID)
	}
}

func TestDecoderRejectsOversizedEvent(t *testing.T) {
	decoder := NewDecoder(strings.NewReader("data: 123456789\n\n"), 8)
	if _, err := decoder.Next(); err == nil {
		t.Fatal("expected size error")
	}
}

func TestDecoderIgnoresInvalidRetryAndNULID(t *testing.T) {
	decoder := NewDecoder(strings.NewReader("id: bad\x00id\nretry: nope\ndata: ok\n\n"), 0)
	event, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	if event.ID != "" || event.Retry != 0 {
		t.Fatalf("unexpected event metadata: %#v", event)
	}
}
