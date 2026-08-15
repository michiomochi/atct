package rpc

import "encoding/json"

// Request and Response use newline-delimited JSON over a Unix socket.
// Herdr uses the same proven pattern.
type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type Response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}
