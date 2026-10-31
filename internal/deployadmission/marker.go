package deployadmission

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

const maxMarkerBytes = 4096

type marker struct {
	Format               uint64 `json:"format"`
	MinimumAuthorization uint64 `json:"minimum_authorization"`
	Phase                string `json:"phase"`
	Lane                 string `json:"lane"`
	Generation           uint64 `json:"generation"`
	OperationNonce       string `json:"operation_nonce"`
	DataDevice           uint64 `json:"data_device"`
	DataInode            uint64 `json:"data_inode"`
	LockDevice           uint64 `json:"lock_device"`
	LockInode            uint64 `json:"lock_inode"`
	BinarySHA256         string `json:"binary_sha256"`
	Role                 string `json:"role"`
	Image                string `json:"image"`
	ReleaseSHA           string `json:"release_sha"`
}

func parseMarker(body []byte) (marker, error) {
	var m marker
	bad := func() (marker, error) { return marker{}, denied("invalid marker") }
	if len(body) == 0 || len(body) > maxMarkerBytes {
		return bad()
	}
	d := json.NewDecoder(bytes.NewReader(body))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return bad()
	}
	fields := make(map[string]json.RawMessage, 14)
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return bad()
		}
		key, ok := token.(string)
		if !ok || !markerField(key) {
			return bad()
		}
		if _, exists := fields[key]; exists {
			return bad()
		}
		var value json.RawMessage
		if err = d.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return bad()
		}
		fields[key] = value
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') || len(fields) != 14 {
		return bad()
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return bad()
	}
	canonical, err := json.Marshal(fields)
	if err != nil {
		return bad()
	}
	if json.Unmarshal(canonical, &m) != nil {
		return bad()
	}
	if m.Format != 1 || m.MinimumAuthorization != 41 || m.Phase != "active" ||
		(m.Lane != "beta" && m.Lane != "production") || m.Generation == 0 ||
		!lowerHex(m.OperationNonce, 64) || m.DataDevice == 0 || m.DataInode == 0 ||
		m.LockDevice == 0 || m.LockInode == 0 || !lowerHex(m.BinarySHA256, 64) ||
		(m.Role != "candidate" && m.Role != "recovery") || !lowerHex(m.ReleaseSHA, 40) {
		return bad()
	}
	const prefix = "ghcr.io/kanterlabs/helm@sha256:"
	if !strings.HasPrefix(m.Image, prefix) || !lowerHex(strings.TrimPrefix(m.Image, prefix), 64) {
		return bad()
	}
	return m, nil
}

func markerField(key string) bool {
	switch key {
	case "format", "minimum_authorization", "phase", "lane", "generation", "operation_nonce", "data_device", "data_inode", "lock_device", "lock_inode", "binary_sha256", "role", "image", "release_sha":
		return true
	}
	return false
}

func lowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
