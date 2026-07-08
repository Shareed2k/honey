package jsonutil

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMarshalUnmarshal(t *testing.T) {
	t.Parallel()

	data := map[string]interface{}{
		"key": "value",
		"num": 42.0,
	}

	b, err := Marshal(data)
	assert.NoError(t, err)
	assert.NotEmpty(t, b)

	var decoded map[string]interface{}
	err = Unmarshal(b, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, data, decoded)
}

func TestMarshalIndent(t *testing.T) {
	t.Parallel()

	data := map[string]interface{}{
		"key": "value",
	}

	b, err := MarshalIndent(data, "", "  ")
	assert.NoError(t, err)
	assert.Contains(t, string(b), "  \"key\": \"value\"")
}

func TestNewEncoder(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	enc := NewEncoder(&buf)

	data := map[string]interface{}{
		"key": "value",
	}
	err := enc.Encode(data)
	assert.NoError(t, err)

	assert.Contains(t, buf.String(), `"key":"value"`)
}
