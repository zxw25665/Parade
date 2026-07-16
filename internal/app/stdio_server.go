package app

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"sync"
)

// StdioServer implements IPCServer over stdin/stdout using newline-delimited
// JSON-RPC 2.0. It also implements IPCClientHub for broadcasting events on
// the same stdout stream.
type StdioServer struct {
	mu     sync.Mutex
	writer *bufio.Writer
	done   chan struct{}
	exited chan struct{}
}

// NewStdioServer creates a new StdioServer with a buffered writer on os.Stdout.
func NewStdioServer() *StdioServer {
	return &StdioServer{
		writer: bufio.NewWriter(os.Stdout),
		done:   make(chan struct{}),
		exited: make(chan struct{}),
	}
}

// Hub returns self — StdioServer is its own IPCClientHub.
func (s *StdioServer) Hub() IPCClientHub {
	return s
}

// EventHub returns nil — events are multiplexed on the same stdout stream.
func (s *StdioServer) EventHub() IPCClientHub {
	return nil
}

// Start redirects Go logging to stderr and begins the stdin read loop in a
// background goroutine.
func (s *StdioServer) Start() error {
	log.SetOutput(os.Stderr)

	go s.readLoop()
	return nil
}

// Stop signals the read loop to stop via the done channel.
func (s *StdioServer) Stop() {
	close(s.done)
}

// readLoop scans os.Stdin line-by-line. Each line is dispatched in its own
// goroutine. On EOF or scanner error, the process exits immediately.
func (s *StdioServer) readLoop() {
	defer close(s.exited)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024*64), 1024*1024)

	for scanner.Scan() {
		data := scanner.Bytes()
		msg := make([]byte, len(data))
		copy(msg, data)

		go s.dispatch(msg)
	}

	// If Stop() was called, return cleanly instead of exiting.
	select {
	case <-s.done:
		return
	default:
	}

	os.Exit(0)
}

// dispatch unmarshals a raw JSON-RPC 2.0 request, looks up the method in
// registeredMethods, invokes the handler, and writes the result or error to
// the buffered stdout writer with an explicit flush after every write.
func (s *StdioServer) dispatch(raw []byte) {
	var req JSONRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		s.mu.Lock()
		WriteJSONRPCError(s.writer, nil, -32700, "Parse error: invalid JSON")
		s.writer.Flush()
		s.mu.Unlock()
		return
	}

	if req.Method == "" {
		s.mu.Lock()
		WriteJSONRPCError(s.writer, req.ID, -32600, "Invalid Request: method is required")
		s.writer.Flush()
		s.mu.Unlock()
		return
	}

	handler, ok := registeredMethods[req.Method]
	if !ok {
		s.mu.Lock()
		WriteJSONRPCError(s.writer, req.ID, -32601, "Method not found: "+req.Method)
		s.writer.Flush()
		s.mu.Unlock()
		return
	}

	result, err := handler(req.Params)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		WriteJSONRPCError(s.writer, req.ID, -1, err.Error())
	} else {
		WriteJSONRPCResult(s.writer, req.ID, result)
	}
	s.writer.Flush()
}

// Broadcast writes a raw payload followed by a newline to the buffered stdout
// writer and flushes immediately. Write errors are logged.
func (s *StdioServer) Broadcast(payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.writer.Write(append(payload, '\n')); err != nil {
		log.Printf("[stdio] broadcast write error: %v", err)
		return
	}
	if err := s.writer.Flush(); err != nil {
		log.Printf("[stdio] broadcast flush error: %v", err)
	}
}
