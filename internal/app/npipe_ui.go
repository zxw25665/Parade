//go:build windows

package app

import (
	"encoding/json"
	"log"
	"sync"

	"golang.org/x/sys/windows"
)

const (
	pipeBufferSize = 64 * 1024
	defaultPipeName = "parade"
)

type NPipeHub struct {
	pipeName string
	clients  map[windows.Handle]*npipeClient
	mu       sync.RWMutex
	done     chan struct{}
}

type npipeClient struct {
	handle windows.Handle
	hub    *NPipeHub
}

func NewNPipeServer(name string) *NPipeServer {
	if name == "" {
		name = defaultPipeName
	}
	return &NPipeServer{
		hub: &NPipeHub{
			pipeName: name,
			clients:  make(map[windows.Handle]*npipeClient),
			done:     make(chan struct{}),
		},
		eventHub: &NPipeHub{
			pipeName: name + "_events",
			clients:  make(map[windows.Handle]*npipeClient),
			done:     make(chan struct{}),
		},
	}
}

type NPipeServer struct {
	hub      *NPipeHub
	eventHub *NPipeHub
}

func (s *NPipeServer) Hub() IPCClientHub {
	return s.hub
}

func (s *NPipeServer) EventHub() IPCClientHub {
	return s.eventHub
}

func (s *NPipeServer) Start() error {
	go s.hub.listenLoop()
	go s.eventHub.listenLoopEvents()
	log.Printf("[npipe] listening on pipe %s (events: %s)", s.hub.pipeName, s.eventHub.pipeName)
	return nil
}

func (s *NPipeServer) Stop() {
	close(s.hub.done)
	close(s.eventHub.done)

	s.hub.mu.Lock()
	for handle := range s.hub.clients {
		windows.CloseHandle(handle)
	}
	s.hub.clients = nil
	s.hub.mu.Unlock()

	s.eventHub.mu.Lock()
	for handle := range s.eventHub.clients {
		windows.CloseHandle(handle)
	}
	s.eventHub.clients = nil
	s.eventHub.mu.Unlock()
}

func (h *NPipeHub) listenLoop() {
	for {
		select {
		case <-h.done:
			return
		default:
		}

		pipePath, _ := windows.UTF16PtrFromString(`\\.\pipe\` + h.pipeName)

		handle, err := windows.CreateNamedPipe(
			pipePath,
			windows.PIPE_ACCESS_DUPLEX,
			windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
			windows.PIPE_UNLIMITED_INSTANCES,
			pipeBufferSize,
			pipeBufferSize,
			0,
			nil,
		)
		if err != nil {
			log.Printf("[npipe] CreateNamedPipe error: %v", err)
			continue
		}

		err = windows.ConnectNamedPipe(handle, nil)
		if err != nil && err != windows.ERROR_PIPE_CONNECTED {
			log.Printf("[npipe] ConnectNamedPipe error: %v", err)
			windows.CloseHandle(handle)
			continue
		}

		client := &npipeClient{handle: handle, hub: h}
		h.mu.Lock()
		h.clients[handle] = client
		h.mu.Unlock()

		log.Printf("[npipe] client connected, handle=%d", handle)
		go h.handleClient(client)
	}
}

func (h *NPipeHub) handleClient(client *npipeClient) {
	defer func() {
		h.mu.Lock()
		delete(h.clients, client.handle)
		h.mu.Unlock()
		windows.CloseHandle(client.handle)
		log.Printf("[npipe] client disconnected, handle=%d", client.handle)
	}()

	readBuf := make([]byte, 1024*64)
	offset := 0

	for {
		select {
		case <-h.done:
			return
		default:
		}

		var n uint32
		err := windows.ReadFile(client.handle, readBuf[offset:], &n, nil)
		if err != nil {
			if err == windows.ERROR_BROKEN_PIPE {
				return
			}
			log.Printf("[npipe] ReadFile error: %v", err)
			return
		}

		if n == 0 {
			continue
		}

		total := offset + int(n)
		offset = 0

		for i := 0; i < total; i++ {
			if readBuf[i] == '\n' {
				line := make([]byte, i-offset)
				copy(line, readBuf[offset:i])
				go h.dispatch(client, line)
				offset = i + 1
			}
		}

		if offset > 0 && offset < total {
			copy(readBuf[:total-offset], readBuf[offset:total])
		}
		offset = total - offset
	}
}

func (h *NPipeHub) dispatch(client *npipeClient, raw []byte) {
	var req JSONRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		log.Printf("[npipe] invalid JSON-RPC request: %v", err)
		WriteJSONRPCError(&npipeWriter{client.handle}, nil, -32700, "Parse error: invalid JSON")
		return
	}

	if req.Method == "" {
		WriteJSONRPCError(&npipeWriter{client.handle}, req.ID, -32600, "Invalid Request: method is required")
		return
	}

	handler, ok := registeredMethods[req.Method]
	if !ok {
		WriteJSONRPCError(&npipeWriter{client.handle}, req.ID, -32601, "Method not found: "+req.Method)
		return
	}

	result, err := handler(req.Params)
	if err != nil {
		WriteJSONRPCError(&npipeWriter{client.handle}, req.ID, -1, err.Error())
		return
	}

	WriteJSONRPCResult(&npipeWriter{client.handle}, req.ID, result)
}

type npipeWriter struct {
	handle windows.Handle
}

func (w *npipeWriter) Write(p []byte) (int, error) {
	var n uint32
	err := windows.WriteFile(w.handle, p, &n, nil)
	if err != nil {
		return int(n), err
	}
	return int(n), nil
}

func (h *NPipeHub) listenLoopEvents() {
	// Event-only pipe: server writes events, clients only read.
	// No handleClient goroutine — data written by Broadcast is consumed
	// by the remote event reader (Tauri Rust side), not by the Go side.
	for {
		select {
		case <-h.done:
			return
		default:
		}

		pipePath, _ := windows.UTF16PtrFromString(`\\.\pipe\` + h.pipeName)

		handle, err := windows.CreateNamedPipe(
			pipePath,
			windows.PIPE_ACCESS_OUTBOUND,
			windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
			windows.PIPE_UNLIMITED_INSTANCES,
			pipeBufferSize,
			pipeBufferSize,
			0,
			nil,
		)
		if err != nil {
			log.Printf("[npipe] CreateNamedPipe(event) error: %v", err)
			continue
		}

		err = windows.ConnectNamedPipe(handle, nil)
		if err != nil && err != windows.ERROR_PIPE_CONNECTED {
			log.Printf("[npipe] ConnectNamedPipe(event) error: %v", err)
			windows.CloseHandle(handle)
			continue
		}

		h.mu.Lock()
		h.clients[handle] = &npipeClient{handle: handle, hub: h}
		h.mu.Unlock()

		log.Printf("[npipe] event client connected, handle=%d", handle)
	}
}

func (h *NPipeHub) Broadcast(payload []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	msg := append(payload, '\n')
	for handle := range h.clients {
		var n uint32
		err := windows.WriteFile(handle, msg, &n, nil)
		if err != nil {
			log.Printf("[npipe] broadcast write error: %v", err)
		}
	}
}
