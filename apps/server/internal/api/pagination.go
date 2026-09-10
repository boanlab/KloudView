package api

import (
	"encoding/base64"
	"strings"
)

func encodeCursor(parts ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, "\x00")))
}

func decodeCursor(value string, count int) ([]string, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, false
	}
	parts := strings.Split(string(decoded), "\x00")
	return parts, len(parts) == count
}
