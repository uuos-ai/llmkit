// Package sse implements provider-neutral Server-Sent Events framing.
package sse

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const defaultMaxEventBytes = 1 << 20

type Event struct {
	Type  string
	Data  []byte
	ID    string
	Retry time.Duration
}

type Decoder struct {
	reader        *bufio.Reader
	maxEventBytes int
	lastEventID   string
	eof           bool
}

func NewDecoder(reader io.Reader, maxEventBytes int) *Decoder {
	if maxEventBytes <= 0 {
		maxEventBytes = defaultMaxEventBytes
	}
	return &Decoder{
		reader:        bufio.NewReader(reader),
		maxEventBytes: maxEventBytes,
	}
}

func (d *Decoder) Next() (Event, error) {
	if d.eof {
		return Event{}, io.EOF
	}

	event := Event{ID: d.lastEventID}
	var data []string
	size := 0
	sawField := false

	for {
		line, err := d.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				d.eof = true
				if !sawField {
					return Event{}, io.EOF
				}
				return finalize(event, data), nil
			}
			return Event{}, err
		}
		size += len(line)
		if size > d.maxEventBytes {
			return Event{}, fmt.Errorf("sse event exceeds %d bytes", d.maxEventBytes)
		}
		if line == "" {
			if !sawField {
				continue
			}
			return finalize(event, data), nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}

		sawField = true
		field, value, found := strings.Cut(line, ":")
		if found && strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		switch field {
		case "event":
			event.Type = value
		case "data":
			data = append(data, value)
		case "id":
			if !strings.ContainsRune(value, '\x00') {
				event.ID = value
				d.lastEventID = value
			}
		case "retry":
			milliseconds, parseErr := strconv.ParseInt(value, 10, 64)
			if parseErr == nil && milliseconds >= 0 {
				event.Retry = time.Duration(milliseconds) * time.Millisecond
			}
		}
	}
}

func (d *Decoder) readLine() (string, error) {
	line, err := d.reader.ReadString('\n')
	if len(line) > d.maxEventBytes {
		return "", fmt.Errorf("sse line exceeds %d bytes", d.maxEventBytes)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	if errors.Is(err, io.EOF) {
		if line == "" {
			return "", io.EOF
		}
		return line, nil
	}
	return line, err
}

func finalize(event Event, data []string) Event {
	event.Data = []byte(strings.Join(data, "\n"))
	return event
}
