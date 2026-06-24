package transferagent

import (
	"fmt"
	"path/filepath"

	"go.uber.org/zap"
)

// buildAgentMaybeUPX cross-compiles honey-transfer-agent under root and optionally packs with UPX.
// On success returns the executable path and cache key to store in resolvedByKey.
func buildAgentMaybeUPX(
	root, cacheDir string,
	useUPX bool,
	targetOS, targetArch, flavor string,
	binPath, cacheKey string,
) (finalPath string, finalKey string, err error) {
	if err := buildTransferAgentBinary(root, binPath, targetOS, targetArch); err != nil {
		return "", cacheKey, err
	}
	if err := ensureExecutable(binPath); err != nil {
		return "", cacheKey, err
	}
	zap.L().Debug(
		"transfer agent binary built",
		zap.String("target", targetOS+"/"+targetArch),
		zap.String("provider_flavor", flavor),
		zap.String("path", binPath),
	)
	if !useUPX {
		return binPath, cacheKey, nil
	}
	if err := packBinaryWithUPX(binPath, targetOS); err != nil {
		zap.L().Warn(
			"transfer agent upx packing failed, using uncompressed binary",
			zap.String("path", binPath),
			zap.String("provider_flavor", flavor),
			zap.Error(err),
		)
		nk := targetOS + "/" + targetArch + "/" + flavor
		nn := fmt.Sprintf("honey-transfer-agent-%s-%s-%s", targetOS, targetArch, flavor)
		np := filepath.Join(cacheDir, nn)
		if err := buildTransferAgentBinary(root, np, targetOS, targetArch); err != nil {
			return "", nk, fmt.Errorf("rebuild uncompressed honey-transfer-agent: %w", err)
		}
		if err := ensureExecutable(np); err != nil {
			return "", nk, err
		}
		return np, nk, nil
	}
	zap.L().Debug(
		"transfer agent binary packed with upx",
		zap.String("target", targetOS+"/"+targetArch),
		zap.String("provider_flavor", flavor),
		zap.String("path", binPath),
	)
	return binPath, cacheKey, nil
}
