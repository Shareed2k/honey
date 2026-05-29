//go:build cgo && (linux || darwin) && (amd64 || arm64)

// Package jsonutil provides a thin JSON shim: sonic on linux/darwin amd64+arm64
// with CGO, encoding/json everywhere else.
package jsonutil

import (
	"io"

	"github.com/bytedance/sonic"
)

// Encoder is satisfied by both sonic.Encoder and json.Encoder.
type Encoder interface{ Encode(v any) error }

// Marshal encodes v to JSON.
func Marshal(v any) ([]byte, error) { return sonic.Marshal(v) }

// Unmarshal decodes JSON data into v.
func Unmarshal(b []byte, v any) error { return sonic.Unmarshal(b, v) }

// MarshalIndent encodes v to indented JSON.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return sonic.MarshalIndent(v, prefix, indent)
}

// NewEncoder returns a streaming JSON encoder writing to w.
func NewEncoder(w io.Writer) Encoder { return sonic.ConfigDefault.NewEncoder(w) }
