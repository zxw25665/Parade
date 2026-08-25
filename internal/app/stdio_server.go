package app

import (
	"bufio"
	"encoding/json"
	"fmt"
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

	stopOnce   sync.Once      // makes Stop idempotent
	dispatchMu sync.Mutex     // serializes the done-check with wg.Add
	wg         sync.WaitGroup // tracks in-flight dispatch goroutines
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

// Stop signals the read loop to stop accepting new work and blocks until all
// in-flight dispatches have drained, so no response is lost on shutdown. It
// is idempotent and safe to call concurrently.
func (s *StdioServer) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
	})

	// The read loop checks done under dispatchMu before wg.Add, so once this
	// critical section completes, no new dispatch can be added. Wait for the
	// dispatches that were already accepted to finish writing their responses.
	s.dispatchMu.Lock()
	s.dispatchMu.Unlock()
	s.wg.Wait()
}

// Exited returns a channel that is closed once the read loop has fully exited
// and drained all in-flight dispatches. The caller owns the process-level
// exit decision (e.g. on stdin EOF), since the server itself never calls
// os.Exit.
func (s *StdioServer) Exited() <-chan struct{} {
	return s.exited
}

// readLoop scans os.Stdin line-by-line. Each line is dispatched in its own
// goroutine. On EOF or scanner error — or after Stop — it drains in-flight
// dispatches and returns cleanly, leaving the exit decision to the caller.
func (s *StdioServer) readLoop() {
	defer close(s.exited)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024*64), 1024*1024)

scanLoop:
	for scanner.Scan() {
		data := scanner.Bytes()
		msg := make([]byte, len(data))
		copy(msg, data)

		// Serialize the done-check with wg.Add so Stop cannot race a
		// dispatch that starts after the check. Tracking lives here, not in
		// dispatch, so callers may invoke dispatch directly (as tests do)
		// without touching the wait group.
		s.dispatchMu.Lock()
		select {
		case <-s.done:
			s.dispatchMu.Unlock()
			break scanLoop
		default:
		}
		s.wg.Add(1)
		s.dispatchMu.Unlock()

		go func() {
			defer s.wg.Done()
			s.dispatch(msg)
		}()
	}

	// No new dispatch can start from here on. Drain in-flight work so every
	// accepted request gets a response before exit is reported.
	s.wg.Wait()
}

// dispatch unmarshals a raw JSON-RPC 2.0 request, looks up the method in
// registeredMethods, invokes the handler, and writes the result or error to
// the buffered stdout writer with an explicit flush after every write.
// It is deliberately not wait-group-aware: the read loop tracks spawned
// dispatches so direct callers (tests) are unaffected.
func (s *StdioServer) dispatch(raw []byte) {
	var req JSONRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		s.writeError(nil, -32700, "Parse error: invalid JSON")
		return
	}

	if req.Method == "" {
		s.writeError(req.ID, -32600, "Invalid Request: method is required")
		return
	}

	handler, ok := registeredMethods[req.Method]
	if !ok {
		s.writeError(req.ID, -32601, "Method not found: "+req.Method)
		return
	}

	result, err, panicked := s.invoke(handler, req.Params)
	if panicked {
		s.writeError(req.ID, -32603, "Internal error: "+err.Error())
		return
	}
	if err != nil {
		s.writeError(req.ID, -1, err.Error())
		return
	}
	s.writeResult(req.ID, result)
}

// invoke runs the handler, converting a panic into an error so a panicking
// handler cannot crash the process. panicked reports that the handler
// panicked (as opposed to returning an error).
func (s *StdioServer) invoke(handler MethodHandler, params json.RawMessage) (result interface{}, err error, panicked bool) {
	panicked = true
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panic: %v", r)
			return
		}
		panicked = false
	}()
	result, err = handler(params)
	return
}

// writeError serializes and flushes a JSON-RPC error response.
func (s *StdioServer) writeError(id json.RawMessage, code int, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	WriteJSONRPCError(s.writer, id, code, message)
	s.writer.Flush()
}

// writeResult serializes and flushes a JSON-RPC result response.
func (s *StdioServer) writeResult(id json.RawMessage, result interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	WriteJSONRPCResult(s.writer, id, result)
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
