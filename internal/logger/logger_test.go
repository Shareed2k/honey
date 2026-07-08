package logger

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestInit(t *testing.T) {
	t.Run("empty_file", func(t *testing.T) {
		err := Init("")
		assert.NoError(t, err)
		assert.Equal(t, zap.NewNop(), zap.L())
	})

	t.Run("valid_file", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "logger_test_*.log")
		assert.NoError(t, err)
		defer os.Remove(tmpFile.Name())
		tmpFile.Close()

		err = Init(tmpFile.Name())
		assert.NoError(t, err)
		assert.NotEqual(t, zap.NewNop(), zap.L())

		// Test Sync doesn't panic
		Sync()
	})

	t.Run("invalid_file", func(t *testing.T) {
		// Try to write to a directory that does not exist to force an error
		err := Init("/path/that/does/not/exist/test.log")
		assert.ErrorContains(t, err, "failed to initialize zap logger")
	})
}
