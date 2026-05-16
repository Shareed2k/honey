//go:build !wasip1 && !wasm

package pluginpdk

import "errors"

var errKVWasmOnly = errors.New("pluginpdk: KV functions require building with GOOS=wasip1 GOARCH=wasm")

// KVGet reads a key from the recipe stepkv store (WASM build only).
func KVGet(string) (string, bool, error) {
	return "", false, errKVWasmOnly
}

// KVPut stores value under key (WASM build only).
func KVPut(string, string) error {
	return errKVWasmOnly
}

// KVDelete removes key (WASM build only).
func KVDelete(string) error {
	return errKVWasmOnly
}
