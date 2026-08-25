package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
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

// TestStdioServerStopIdempotent verifies that Stop can be called any number
// of times, sequentially or concurrently, without panicking.
func TestStdioServerStopIdempotent(t *testing.T) {
	server := NewStdioServer()

	server.Stop()
	server.Stop()
	server.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			server.Stop()
		}()
	}
	wg.Wait()
}

// TestStdioServerHandlerPanic verifies that a panicking handler is converted
// into a JSON-RPC internal error (-32603) instead of crashing the process.
func TestStdioServerHandlerPanic(t *testing.T) {
	restore := saveGlobals()
	defer restore()

	registeredMethods = map[string]MethodHandler{
		"boom": func(params json.RawMessage) (any, error) {
			panic("kaboom")
		},
	}

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	server := NewStdioServer()
	server.writer = bufio.NewWriter(stdoutW)

	server.dispatch([]byte(`{"jsonrpc":"2.0","id":7,"method":"boom"}`))

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
		if code != -32603 {
			t.Fatalf("expected internal error code -32603, got %v", code)
		}
		msg, _ := errData["message"].(string)
		if !strings.Contains(msg, "kaboom") {
			t.Fatalf("expected panic message in error, got: %v", errData["message"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for error response")
	}
}

// TestStdioServerStopDrainsInFlight verifies that Stop blocks until every
// in-flight dispatch has completed and written its response, so the caller
// can exit without losing responses.
func TestStdioServerStopDrainsInFlight(t *testing.T) {
	restore := saveGlobals()
	defer restore()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = stdinR

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	registeredMethods = map[string]MethodHandler{
		"slow": func(params json.RawMessage) (any, error) {
			close(started)
			<-release
			return "done", nil
		},
	}

	server := NewStdioServer()
	server.writer = bufio.NewWriter(stdoutW)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}

	if _, err := stdinW.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"slow"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	<-started // handler is now in flight

	stopDone := make(chan struct{})
	go func() {
		server.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		t.Fatal("Stop returned while a dispatch was still in flight")
	case <-time.After(100 * time.Millisecond):
		// Stop is blocked on the in-flight dispatch, as required.
	}

	close(release)

	select {
	case <-stopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after the in-flight dispatch finished")
	}

	// The drained dispatch must have flushed its response before Stop
	// returned, so it is already readable on stdout.
	scanner := bufio.NewScanner(stdoutR)
	if !scanner.Scan() {
		t.Fatal("expected the drained dispatch's response on stdout")
	}
	var resp map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("drained response unmarshal: %v", err)
	}
	if resp["result"] != "done" {
		t.Fatalf("expected result 'done', got %v", resp["result"])
	}

	// Deliver EOF so the read loop can exit; Exited must close.
	stdinW.Close()
	select {
	case <-server.Exited():
	case <-time.After(2 * time.Second):
		t.Fatal("read loop did not exit after EOF")
	}
}

// stdioServerEOFChild runs in a subprocess: it starts a StdioServer, delivers
// EOF on stdin, and prints a marker only after the read loop has exited
// cleanly. If the read loop calls os.Exit on EOF, the marker is never printed
// and the parent test fails.
func stdioServerEOFChild(t *testing.T) {
	restore := saveGlobals()
	defer restore()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = stdinR

	server := NewStdioServer()
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}

	stdinW.Close() // deliver EOF

	select {
	case <-server.Exited():
	case <-time.After(3 * time.Second):
		t.Fatal("read loop did not exit after EOF")
	}

	// Reaching this line proves the read loop returned instead of calling
	// os.Exit. The parent asserts on this marker.
	fmt.Println("readLoop-exited-cleanly")
}

// TestStdioServerEOFExitsCleanly proves that EOF on stdin makes the read loop
// return without terminating the process. It runs the server in a child
// process, because the old implementation called os.Exit(0) which would
// silently kill the test binary.
func TestStdioServerEOFExitsCleanly(t *testing.T) {
	if os.Getenv("STDIO_SERVER_EOF_CHILD") == "1" {
		stdioServerEOFChild(t)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestStdioServerEOFExitsCleanly")
	cmd.Env = append(os.Environ(), "STDIO_SERVER_EOF_CHILD=1")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The child replaces os.Stdin with its own pipe; close our end so the
	// child never blocks on the harness pipe.
	stdin.Close()

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("child exited with error: %v\nstderr: %s", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("child did not exit after stdin EOF (read loop hung?)")
	}

	if !strings.Contains(stdout.String(), "readLoop-exited-cleanly") {
		t.Fatalf("child exited before the read loop returned cleanly (os.Exit in read loop?)\nstdout: %s\nstderr: %s",
			stdout.String(), stderr.String())
	}
}
