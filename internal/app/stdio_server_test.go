package app

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"testing"
	"time"
)

// saveGlobals returns cleanup functions to restore os.Stdin, os.Stdout,
// log output, and registeredMethods.
func saveGlobals() (restore func()) {
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	oldMethods := registeredMethods
	oldLogOut := log.Writer()
	return func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
		registeredMethods = oldMethods
		log.SetOutput(oldLogOut)
	}
}

// TestStdioServerRPC validates a full RPC call round-trip through the server.
func TestStdioServerRPC(t *testing.T) {
	restore := saveGlobals()
	defer restore()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	os.Stdin = stdinR
	os.Stdout = stdoutW

	registeredMethods = map[string]MethodHandler{
		"echo": func(params json.RawMessage) (any, error) {
			var args []string
			if err := json.Unmarshal(params, &args); err != nil {
				return nil, err
			}
			return args[0], nil
		},
	}

	server := NewStdioServer()
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}

	// Write the request to stdin.
	if _, err := stdinW.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"echo","params":["hello"]}` + "\n")); err != nil {
		t.Fatal(err)
	}

	// Read the response from stdout pipe.
	resultCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdoutR)
		if scanner.Scan() {
			resultCh <- scanner.Text()
		}
	}()

	select {
	case raw := <-resultCh:
		var resp map[string]any
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp["error"] != nil {
			t.Fatalf("unexpected error: %v", resp["error"])
		}
		result, ok := resp["result"].(string)
		if !ok {
			t.Fatalf("expected string result, got %T: %v", resp["result"], resp["result"])
		}
		if result != "hello" {
			t.Fatalf("expected result 'hello', got %q", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for response")
	}

	// Clean shutdown: close the write end to unblock the scanner, then
	// wait for the readLoop goroutine to exit gracefully.
	server.Stop()
	stdinW.Close()
	<-server.exited
}

// TestStdioServerBroadcast validates that Broadcast writes the payload to
// stdout followed by a newline and flushes.
func TestStdioServerBroadcast(t *testing.T) {
	restore := saveGlobals()
	defer restore()

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	server := NewStdioServer()
	server.writer = bufio.NewWriter(stdoutW)

	payload := []byte(`{"jsonrpc":"2.0","method":"event","params":{"event":"test"}}`)
	server.Broadcast(payload)

	resultCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdoutR)
		if scanner.Scan() {
			resultCh <- scanner.Text()
		}
	}()

	select {
	case raw := <-resultCh:
		if raw != string(payload) {
			t.Fatalf("expected %q, got %q", string(payload), raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for broadcast")
	}
}

// TestStdioServerInvalidJSON validates that invalid JSON produces a parse
// error response with code -32700.
func TestStdioServerInvalidJSON(t *testing.T) {
	restore := saveGlobals()
	defer restore()

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	server := NewStdioServer()
	server.writer = bufio.NewWriter(stdoutW)

	server.dispatch([]byte("not json"))

	resultCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdoutR)
		if scanner.Scan() {
			resultCh <- scanner.Text()
		}
	}()

	select {
	case raw := <-resultCh:
		var resp map[string]any
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		errData, ok := resp["error"].(map[string]any)
		if !ok {
			t.Fatalf("expected error object in response, got: %v", raw)
		}
		code, ok := errData["code"].(float64)
		if !ok {
			t.Fatalf("expected numeric error code, got: %v", errData["code"])
		}
		if code != -32700 {
			t.Fatalf("expected error code -32700, got %v", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for error response")
	}
}

// TestStdioServerUnknownMethod validates that a valid JSON-RPC request with
// an unknown method produces error code -32601.
func TestStdioServerUnknownMethod(t *testing.T) {
	restore := saveGlobals()
	defer restore()

	registeredMethods = map[string]MethodHandler{}

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	server := NewStdioServer()
	server.writer = bufio.NewWriter(stdoutW)

	server.dispatch([]byte(`{"jsonrpc":"2.0","id":1,"method":"nonexistent","params":[]}`))

	resultCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdoutR)
		if scanner.Scan() {
			resultCh <- scanner.Text()
		}
	}()

	select {
	case raw := <-resultCh:
		var resp map[string]any
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		errData, ok := resp["error"].(map[string]any)
		if !ok {
			t.Fatalf("expected error object in response, got: %v", raw)
		}
		code, ok := errData["code"].(float64)
		if !ok {
			t.Fatalf("expected numeric error code, got: %v", errData["code"])
		}
		if code != -32601 {
			t.Fatalf("expected error code -32601, got %v", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for error response")
	}
}

// TestStdioServerStartStop verifies that the server can be created,
// Hub/EventHub return expected values, and Start/Stop do not panic.
// To avoid spawning a goroutine that reads from real os.Stdin (which
// may cause os.Exit(0) in non-interactive environments), Start is
// tested with a pipe-based stdin so the scanner blocks safely.
func TestStdioServerStartStop(t *testing.T) {
	server := NewStdioServer()

	if server.Hub() != server {
		t.Error("Hub() should return self")
	}
	if server.EventHub() != nil {
		t.Error("EventHub() should return nil")
	}

	// Stop on a non-started server should not panic.
	server.Stop()

	// Test Start + Stop with a controlled stdin.
	restore := saveGlobals()
	defer restore()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = stdinR

	server2 := NewStdioServer()
	if err := server2.Start(); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	server2.Stop()

	// Close the write end to deliver EOF. The goroutine sees EOF,
	// checks the closed done channel, and returns cleanly.
	stdinW.Close()

	// Wait for the readLoop goroutine to actually exit before the
	// test function returns, so it does not read from a subsequently
	// restored os.Stdin.
	<-server2.exited
	_ = stdinR
}

// TestStdioServerEOFExit validates the dispatch loop behaviour by simulating a
// scanner loop that feeds multiple lines through dispatch and verifies each
// response. This exercises the same code path the real readLoop uses without
// triggering os.Exit.
func TestStdioServerEOFExit(t *testing.T) {
	restore := saveGlobals()
	defer restore()

	registeredMethods = map[string]MethodHandler{
		"ping": func(params json.RawMessage) (any, error) {
			return "pong", nil
		},
	}

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	server := NewStdioServer()
	server.writer = bufio.NewWriter(stdoutW)

	// Simulate the scanner dispatch loop: goroutine feeds 3 messages, main
	// goroutine reads each response sequentially.
	go func() {
		server.dispatch([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		server.dispatch([]byte(`{"jsonrpc":"2.0","id":2,"method":"ping"}`))
		server.dispatch([]byte(`{"jsonrpc":"2.0","id":3,"method":"nonexistent"}`))
	}()

	scanner := bufio.NewScanner(stdoutR)

	// Response 1 — success.
	if !scanner.Scan() {
		t.Fatal("expected response 1")
	}
	var r1 map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &r1); err != nil {
		t.Fatalf("response 1 unmarshal: %v", err)
	}
	if r1["result"] != "pong" {
		t.Fatalf("response 1: expected 'pong', got %v", r1["result"])
	}

	// Response 2 — success.
	if !scanner.Scan() {
		t.Fatal("expected response 2")
	}
	var r2 map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &r2); err != nil {
		t.Fatalf("response 2 unmarshal: %v", err)
	}
	if r2["result"] != "pong" {
		t.Fatalf("response 2: expected 'pong', got %v", r2["result"])
	}

	// Response 3 — error (unknown method).
	if !scanner.Scan() {
		t.Fatal("expected response 3")
	}
	var r3 map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &r3); err != nil {
		t.Fatalf("response 3 unmarshal: %v", err)
	}
	errData, ok := r3["error"].(map[string]any)
	if !ok {
		t.Fatalf("response 3: expected error, got %v", scanner.Text())
	}
	code, _ := errData["code"].(float64)
	if code != -32601 {
		t.Fatalf("response 3: expected -32601, got %v", code)
	}
}

// TestStdioServerFlushBehavior validates that the server explicitly flushes
// after each write, ensuring data is immediately available on the pipe.
func TestStdioServerFlushBehavior(t *testing.T) {
	restore := saveGlobals()
	defer restore()

	registeredMethods = map[string]MethodHandler{
		"echo": func(params json.RawMessage) (any, error) {
			var args []string
			if err := json.Unmarshal(params, &args); err != nil {
				return nil, err
			}
			return args[0], nil
		},
	}

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	server := NewStdioServer()
	server.writer = bufio.NewWriter(stdoutW)

	// Write two requests through dispatch without any artificial delay.
	// If flush is working correctly, the scanner on stdoutR will see each
	// line as soon as it's written, not after buffer fills.
	go func() {
		server.dispatch([]byte(`{"jsonrpc":"2.0","id":1,"method":"echo","params":["first"]}`))
		server.dispatch([]byte(`{"jsonrpc":"2.0","id":2,"method":"echo","params":["second"]}`))
	}()

	scanner := bufio.NewScanner(stdoutR)

	if !scanner.Scan() {
		t.Fatal("expected first response immediately (flush working)")
	}
	var r1 map[string]any
	json.Unmarshal(scanner.Bytes(), &r1)
	if r1["result"] != "first" {
		t.Fatalf("expected 'first', got %v", r1["result"])
	}

	if !scanner.Scan() {
		t.Fatal("expected second response immediately (flush working)")
	}
	var r2 map[string]any
	json.Unmarshal(scanner.Bytes(), &r2)
	if r2["result"] != "second" {
		t.Fatalf("expected 'second', got %v", r2["result"])
	}
}
