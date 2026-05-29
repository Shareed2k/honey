//go:build !cgo || (!linux && !darwin) || (!amd64 && !arm64)

package jsonutil

import (
	"encoding/json"
	"io"
)

// Encoder is satisfied by both sonic.Encoder and json.Encoder.
type Encoder interface{ Encode(v any) error }

// Marshal encodes v to JSON.
func Marshal(v any) ([]byte, error) { return json.Marshal(v) }

// Unmarshal decodes JSON data into v.
func Unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// MarshalIndent encodes v to indented JSON.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return json.MarshalIndent(v, prefix, indent)
}

// NewEncoder returns a streaming JSON encoder writing to w.
func NewEncoder(w io.Writer) Encoder { return json.NewEncoder(w) }
