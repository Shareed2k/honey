//go:build !wasip1 && !wasm

package pluginpdk

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKVStub(t *testing.T) {
	t.Run("KVGet", func(t *testing.T) {
		t.Parallel()
		val, found, err := KVGet("key")
		assert.ErrorIs(t, err, errKVWasmOnly)
		assert.False(t, found)
		assert.Empty(t, val)
	})

	t.Run("KVPut", func(t *testing.T) {
		t.Parallel()
		err := KVPut("key", "value")
		assert.ErrorIs(t, err, errKVWasmOnly)
	})

	t.Run("KVDelete", func(t *testing.T) {
		t.Parallel()
		err := KVDelete("key")
		assert.ErrorIs(t, err, errKVWasmOnly)
	})
}
