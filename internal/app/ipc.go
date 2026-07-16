package app

import (
	"encoding/json"
	"io"
)

type IPCServer interface {
	Start() error
	Stop()
	Hub() IPCClientHub
// EventHub returns nil — events are multiplexed on the stdout stream.
// StdioServer uses a single stream for both RPC responses and events.
	EventHub() IPCClientHub
}

type IPCClientHub interface {
	Broadcast(payload []byte)
}

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func WriteJSONRPCResult(w io.Writer, id json.RawMessage, result interface{}) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, _ := json.Marshal(resp)
	w.Write(append(data, '\n'))
}

func WriteJSONRPCError(w io.Writer, id json.RawMessage, code int, message string) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: message},
	}
	data, _ := json.Marshal(resp)
	w.Write(append(data, '\n'))
}
