package httpapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// A confirmation is a five-minute, actor-bound proof of an exact read-only
// preview. This random process-local key is never persisted, logged or exported;
// restart invalidates outstanding previews and requires a fresh review.
var correctionProofKey = func() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("audit preview entropy unavailable")
	}
	return key
}()

type correctionProof struct {
	Actor, Audit, Finding, Fingerprint string
	Expires                            int64
}

func signCorrectionProof(actor, audit, finding, fingerprint string, expires int64) string {
	raw, _ := json.Marshal(correctionProof{actor, audit, finding, fingerprint, expires})
	m := hmac.New(sha256.New, correctionProofKey)
	m.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func verifyCorrectionProof(token, actor, audit, finding string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(token) > 4096 {
		return ""
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return ""
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return ""
	}
	m := hmac.New(sha256.New, correctionProofKey)
	m.Write(raw)
	if !hmac.Equal(sig, m.Sum(nil)) {
		return ""
	}
	var p correctionProof
	if json.Unmarshal(raw, &p) != nil || p.Actor != actor || p.Audit != audit || p.Finding != finding || p.Expires <= time.Now().Unix() || p.Expires > time.Now().Add(5*time.Minute).Unix()+5 {
		return ""
	}
	return p.Fingerprint
}
