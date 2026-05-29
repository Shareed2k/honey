// Package jsonutil wraps goccy/go-json as a drop-in for encoding/json.
package jsonutil

import (
	"io"

	"github.com/goccy/go-json"
)

// Encoder is satisfied by both goccy and encoding/json encoders.
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
