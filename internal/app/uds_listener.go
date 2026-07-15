//go:build !windows

package app

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"os"
	"sync"
)

type UDSHub struct {
	listener net.Listener
	path     string
	clients  map[net.Conn]struct{}
	mu       sync.RWMutex
	done     chan struct{}
}

func NewUDSServer(path string) *UDSServer {
	return &UDSServer{
		hub: &UDSHub{
			path:    path,
			clients: make(map[net.Conn]struct{}),
			done:    make(chan struct{}),
		},
	}
}

type UDSServer struct {
	hub *UDSHub
}

func (s *UDSServer) Hub() IPCClientHub {
	return s.hub
}

func (s *UDSServer) Start() error {
	os.Remove(s.hub.path)

	listener, err := net.Listen("unix", s.hub.path)
	if err != nil {
		return err
	}
	s.hub.listener = listener

	if err := os.Chmod(s.hub.path, 0o600); err != nil {
		log.Printf("[uds] warning: failed to set socket permissions: %v", err)
	}

	log.Printf("[uds] listening on %s", s.hub.path)

	go s.hub.acceptLoop()
	return nil
}

func (s *UDSServer) Stop() {
	close(s.hub.done)
	s.hub.mu.Lock()
	for conn := range s.hub.clients {
		conn.Close()
	}
	s.hub.clients = nil
	s.hub.mu.Unlock()

	if s.hub.listener != nil {
		s.hub.listener.Close()
	}
	os.Remove(s.hub.path)
}

func (h *UDSHub) acceptLoop() {
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			select {
			case <-h.done:
				return
			default:
				log.Printf("[uds] accept error: %v", err)
				continue
			}
		}

		h.mu.Lock()
		h.clients[conn] = struct{}{}
		h.mu.Unlock()

		go h.handleClient(conn)
	}
}

func (h *UDSHub) handleClient(conn net.Conn) {
	defer func() {
		h.mu.Lock()
		delete(h.clients, conn)
		h.mu.Unlock()
		conn.Close()
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 1024*64), 1024*1024)

	for {
		select {
		case <-h.done:
			return
		default:
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				log.Printf("[uds] client read error: %v", err)
			}
			return
		}

		data := scanner.Bytes()
		msg := make([]byte, len(data))
		copy(msg, data)

		go h.dispatch(conn, msg)
	}
}

func (h *UDSHub) dispatch(conn net.Conn, raw []byte) {
	var req jsonRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		log.Printf("[uds] invalid JSON-RPC request: %v", err)
		writeError(conn, nil, -32700, "Parse error: invalid JSON")
		return
	}

	if req.Method == "" {
		writeError(conn, req.ID, -32600, "Invalid Request: method is required")
		return
	}

	handler, ok := registeredMethods[req.Method]
	if !ok {
		writeError(conn, req.ID, -32601, "Method not found: "+req.Method)
		return
	}

	result, err := handler(req.Params)
	if err != nil {
		writeError(conn, req.ID, -1, err.Error())
		return
	}

	writeResult(conn, req.ID, result)
}

func (h *UDSHub) Broadcast(payload []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for conn := range h.clients {
		_, err := conn.Write(append(payload, '\n'))
		if err != nil {
			log.Printf("[uds] broadcast write error: %v", err)
		}
	}
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeResult(conn net.Conn, id json.RawMessage, result interface{}) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, _ := json.Marshal(resp)
	conn.Write(append(data, '\n'))
}

func writeError(conn net.Conn, id json.RawMessage, code int, message string) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	}
	data, _ := json.Marshal(resp)
	conn.Write(append(data, '\n'))
}
