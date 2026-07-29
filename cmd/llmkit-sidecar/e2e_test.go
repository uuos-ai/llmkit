package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/uuos-ai/llmkit/sidecar/protocol"
)

const e2eSessionKey = "e2e-0123456789abcdef0123456789abcdef"

func TestSidecarProcessHandshakeHealthAndShutdown(t *testing.T) {
	binaryName := "llmkit-sidecar-e2e"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(t.TempDir(), binaryName)
	build := exec.Command("go", "build", "-o", binaryPath, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build sidecar: %v\n%s", err, output)
	}

	address := e2eLocalAddress(t)
	command := exec.Command(binaryPath,
		"--socket", address,
		"--parent-pid", strconv.Itoa(os.Getpid()),
	)
	command.Stdin = bytes.NewBufferString(e2eSessionKey + "\n")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}

	connection, err := waitForE2EConnection(address, 5*time.Second)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("connect to sidecar: %v; stderr=%q", err, stderr.String())
	}
	codec := protocol.NewCodec(connection, connection, 0)
	handshakePayload, _ := json.Marshal(protocol.HandshakeRequest{SupportedVersions: []string{protocol.Version}})
	writeE2ERequest(t, codec, "handshake", protocol.MethodHandshake, handshakePayload)
	response := readE2EResponse(t, codec)
	if response.Type != protocol.TypeResult {
		t.Fatalf("handshake response = %#v", response)
	}
	var negotiated protocol.HandshakeResponse
	if err := json.Unmarshal(response.Payload, &negotiated); err != nil {
		t.Fatal(err)
	}
	if !containsMethod(negotiated.Methods, protocol.MethodValidateCredential) ||
		!containsMethod(negotiated.Methods, protocol.MethodListModels) {
		t.Fatalf("negotiated methods = %#v", negotiated.Methods)
	}

	writeE2ERequest(t, codec, "health", protocol.MethodHealth, nil)
	response = readE2EResponse(t, codec)
	if response.Type != protocol.TypeResult {
		t.Fatalf("health response = %#v", response)
	}
	writeE2ERequest(t, codec, "shutdown", protocol.MethodShutdown, nil)
	response = readE2EResponse(t, codec)
	if response.Type != protocol.TypeResult {
		t.Fatalf("shutdown response = %#v", response)
	}
	_ = connection.Close()

	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sidecar exit: %v; stderr=%q", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		<-done
		t.Fatal("sidecar did not shut down")
	}
}

func waitForE2EConnection(address string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		connection, err := dialE2ELocal(address, 100*time.Millisecond)
		if err == nil {
			return connection, nil
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	return nil, fmt.Errorf("local endpoint was not ready: %w", lastErr)
}

func writeE2ERequest(t *testing.T, codec *protocol.Codec, requestID string, method protocol.Method, payload []byte) {
	t.Helper()
	if err := codec.WriteRequest(protocol.Request{
		Version: protocol.Version, SessionKey: e2eSessionKey,
		RequestID: requestID, Method: method, Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
}

func readE2EResponse(t *testing.T, codec *protocol.Codec) protocol.Response {
	t.Helper()
	response, err := codec.ReadResponse()
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func containsMethod(methods []protocol.Method, want protocol.Method) bool {
	for _, method := range methods {
		if method == want {
			return true
		}
	}
	return false
}
