//go:build wasip1 || wasm

package pluginpdk

import (
	"errors"

	"github.com/extism/go-pdk"
)

//go:wasmimport extism:host/user kv
func kvHost(inputOffset uint64) uint64

// KVGet reads a key from the recipe stepkv store.
// Missing keys return found=false and a nil error.
func KVGet(key string) (value string, found bool, err error) {
	out, err := callKV(kvInput{Op: "get", Key: key})
	if err != nil {
		return "", false, err
	}
	return out.Value, out.Found, nil
}

// KVPut stores value under key in the recipe stepkv store.
func KVPut(key, value string) error {
	out, err := callKV(kvInput{Op: "put", Key: key, Value: value})
	if err != nil {
		return err
	}
	if out.Error != "" {
		return errors.New(out.Error)
	}
	return nil
}

// KVDelete removes key from the recipe stepkv store.
func KVDelete(key string) error {
	out, err := callKV(kvInput{Op: "delete", Key: key})
	if err != nil {
		return err
	}
	if out.Error != "" {
		return errors.New(out.Error)
	}
	return nil
}

func callKV(in kvInput) (kvOutput, error) {
	mem, err := pdk.AllocateJSON(in)
	if err != nil {
		return kvOutput{}, err
	}
	off := kvHost(mem.Offset())
	if off == 0 {
		return kvOutput{}, errors.New("kv host function returned 0")
	}
	result := pdk.FindMemory(off)
	return parseKVOutput(result.ReadBytes())
}
