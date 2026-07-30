// Package servicetoken provides reloadable short-lived business API tokens.
package servicetoken

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
)

type FileSource struct{ path string }

func NewFileSource(path string) (*FileSource, error) {
	if path == "" {
		return nil, errors.New("service token: file path is required")
	}
	source := &FileSource{path: path}
	token, err := source.ServiceToken(context.Background())
	clear(token)
	if err != nil {
		return nil, err
	}
	return source, nil
}

func (s *FileSource) ServiceToken(context.Context) ([]byte, error) {
	info, err := os.Stat(s.path)
	if err != nil {
		return nil, errors.New("service token: file could not be inspected")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("service token: file permissions must not grant group or other access")
	}
	file, err := os.Open(s.path)
	if err != nil {
		return nil, errors.New("service token: file could not be opened")
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, 16<<10))
	if err != nil {
		return nil, errors.New("service token: file could not be read")
	}
	trimmed := strings.TrimSpace(string(value))
	clear(value)
	if len(trimmed) < 32 {
		return nil, errors.New("service token: token is too short")
	}
	return []byte(trimmed), nil
}
