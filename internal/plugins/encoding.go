package plugins

import (
	"encoding/base64"
	"fmt"
)

func encodeB64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func decodeB64(s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("empty base64")
	}
	return base64.StdEncoding.DecodeString(s)
}
