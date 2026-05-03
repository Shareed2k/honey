package logger

import (
	"fmt"

	"go.uber.org/zap"
)

// Init configures the global zap logger.
// If logFile is empty, it configures a no-op logger.
func Init(logFile string) error {
	if logFile == "" {
		zap.ReplaceGlobals(zap.NewNop())
		return nil
	}

	cfg := zap.NewDevelopmentConfig()
	cfg.OutputPaths = []string{logFile}
	cfg.ErrorOutputPaths = []string{logFile}

	logger, err := cfg.Build()
	if err != nil {
		return fmt.Errorf("failed to initialize zap logger: %w", err)
	}

	zap.ReplaceGlobals(logger)
	return nil
}

// Sync flushes any buffered log entries.
func Sync() {
	_ = zap.L().Sync()
}
