package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseBytes converts a size string ("5GiB", "64MiB", "1024") into bytes.
// Accepted suffixes (case-insensitive): KiB, MiB, GiB, TiB. Bare integers are
// taken as bytes. Fractional values and SI suffixes (KB, MB, …) are rejected.
func ParseBytes(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	lower := strings.ToLower(s)
	mul := int64(1)
	switch {
	case strings.HasSuffix(lower, "tib"):
		mul = 1 << 40
		s = s[:len(s)-3]
	case strings.HasSuffix(lower, "gib"):
		mul = 1 << 30
		s = s[:len(s)-3]
	case strings.HasSuffix(lower, "mib"):
		mul = 1 << 20
		s = s[:len(s)-3]
	case strings.HasSuffix(lower, "kib"):
		mul = 1 << 10
		s = s[:len(s)-3]
	default:
		// SI suffixes are NOT accepted — reject them explicitly to avoid silent surprises.
		for _, suf := range []string{"tb", "gb", "mb", "kb", "b"} {
			if strings.HasSuffix(lower, suf) {
				return 0, fmt.Errorf("SI byte suffixes (%s) not supported; use KiB/MiB/GiB/TiB", strings.ToUpper(suf))
			}
		}
	}
	s = strings.TrimSpace(s)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse bytes %q: %w", s, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("negative size")
	}
	return n * mul, nil
}
