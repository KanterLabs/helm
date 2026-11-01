package deployadmission

import (
	"encoding/json"
	"strings"
	"testing"
)

func validMarker() marker {
	return marker{Format: 1, MinimumAuthorization: 41, Phase: "active", Lane: "beta",
		Generation: 1, OperationNonce: strings.Repeat("a", 64), DataDevice: 1, DataInode: 2,
		LockDevice: 3, LockInode: 4, BinarySHA256: strings.Repeat("b", 64), Role: "candidate",
		Image: "ghcr.io/kanterlabs/helm@sha256:" + strings.Repeat("c", 64), ReleaseSHA: strings.Repeat("d", 40)}
}

func markerJSON(t *testing.T, m marker) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMarkerStrictJSON(t *testing.T) {
	good := string(markerJSON(t, validMarker()))
	cases := map[string]string{
		"empty": "", "truncated": good[:len(good)-1], "unknown": strings.TrimSuffix(good, "}") + `,"extra":1}`,
		"duplicate": strings.TrimSuffix(good, "}") + `,"format":1}`, "trailing": good + ` {}`,
		"missing": strings.Replace(good, `"format":1,`, "", 1), "null": `null`,
		"oversized": good + strings.Repeat(" ", 4097), "wrong type": strings.Replace(good, `"format":1`, `"format":"1"`, 1),
		"case alias":  strings.Replace(good, `"format"`, `"Format"`, 1),
		"null number": strings.Replace(good, `"format":1`, `"format":null`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseMarker([]byte(body)); err == nil {
				t.Fatal("accepted invalid marker")
			}
		})
	}
	if _, err := parseMarker([]byte(good)); err != nil {
		t.Fatal(err)
	}
}

func TestMarkerContract(t *testing.T) {
	cases := map[string]func(*marker){
		"format": func(m *marker) { m.Format = 2 }, "authorization": func(m *marker) { m.MinimumAuthorization = 40 },
		"phase": func(m *marker) { m.Phase = "closed" }, "lane": func(m *marker) { m.Lane = "public" },
		"generation": func(m *marker) { m.Generation = 0 }, "nonce": func(m *marker) { m.OperationNonce = strings.Repeat("A", 64) },
		"data device": func(m *marker) { m.DataDevice = 0 }, "data inode": func(m *marker) { m.DataInode = 0 },
		"lock device": func(m *marker) { m.LockDevice = 0 }, "lock inode": func(m *marker) { m.LockInode = 0 },
		"binary": func(m *marker) { m.BinarySHA256 = "wrong" }, "role": func(m *marker) { m.Role = "both" },
		"image":   func(m *marker) { m.Image = "ghcr.io/other/helm@sha256:" + strings.Repeat("a", 64) },
		"release": func(m *marker) { m.ReleaseSHA = strings.Repeat("A", 40) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := validMarker()
			mutate(&m)
			if _, err := parseMarker(markerJSON(t, m)); err == nil {
				t.Fatal("accepted invalid contract")
			}
		})
	}
	for _, lane := range []string{"beta", "production"} {
		for _, role := range []string{"candidate", "recovery"} {
			m := validMarker()
			m.Lane = lane
			m.Role = role
			if _, err := parseMarker(markerJSON(t, m)); err != nil {
				t.Fatal(err)
			}
		}
	}
}
