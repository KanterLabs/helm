package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// TailnetAssertionHeader is the only application identity header accepted in
// tailnet mode. The edge forward-auth service is the sole component allowed
// to create this assertion.
const TailnetAssertionHeader = "X-Helm-Tailnet-Assertion"

// TailnetAssertionIssuer identifies this assertion format and prevents a
// token minted for another internal service from being accepted by Helm.
const TailnetAssertionIssuer = "helm-tailnet-v1"

const (
	// TailnetAssertionTTL is intentionally short. The assertion is bound to
	// one request, so a browser does not need a refresh token or a reusable
	// bearer credential.
	TailnetAssertionTTL = 30 * time.Second
	// TailnetAssertionMaxTTL limits both configured and forged lifetimes. A
	// small clock allowance handles normal host clock skew without turning a
	// stale assertion into a session.
	TailnetAssertionMaxTTL  = 2 * time.Minute
	TailnetClockSkew        = 5 * time.Second
	TailnetMaxAssertionSize = 16 * 1024
	TailnetMaxAudienceSize  = 1024
	TailnetMaxURI           = 16 * 1024
)

var tailnetJWTHeader = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"

// TailnetClaims are the signed identity and request-binding fields emitted by
// the loopback helper. All fields are covered by the HMAC signature.
type TailnetClaims struct {
	Issuer     string `json:"iss"`
	Audience   string `json:"aud"`
	Subject    string `json:"sub"`
	Email      string `json:"email"`
	Name       string `json:"name,omitempty"`
	Method     string `json:"method"`
	RequestURI string `json:"request_uri"`
	IssuedAt   int64  `json:"iat"`
	ExpiresAt  int64  `json:"exp"`
}

// TailnetIdentityVerifier is deliberately shaped like the Cloudflare
// verifier interface. The manager performs request-binding and owner checks
// after Verify returns, so an injected verifier cannot accidentally bypass
// those checks.
type TailnetIdentityVerifier interface {
	Verify(context.Context, string) (TailnetClaims, error)
}

// TailnetJWTVerifier verifies compact HS256 assertions. It does not know the
// current HTTP request; callers must compare the returned method, URI, and
// audience with their request before using the identity.
type TailnetJWTVerifier struct {
	key []byte
	now func() time.Time
}

// NewTailnetJWTVerifier validates and copies a dedicated assertion key. A
// 256-bit key is the minimum accepted size for this HMAC boundary.
func NewTailnetJWTVerifier(key []byte) (*TailnetJWTVerifier, error) {
	if len(key) < 32 {
		return nil, errors.New("tailnet assertion key must be at least 32 bytes")
	}
	return &TailnetJWTVerifier{key: append([]byte(nil), key...), now: time.Now}, nil
}

// GenerateTailnetAssertionKey returns a fresh 256-bit key for a protected key
// file. Production callers should persist it in a mode-0600 file and never
// put it in logs or request data.
func GenerateTailnetAssertionKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := cryptorand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// SignTailnetAssertion signs one complete assertion with key. The helper
// should set IssuedAt/ExpiresAt close to the current time and use the exact
// origin, method, and RequestURI supplied by the trusted edge.
func SignTailnetAssertion(key []byte, claims TailnetClaims) (string, error) {
	return SignTailnetAssertionAt(key, claims, time.Now())
}

// SignTailnetAssertionAt is the deterministic-clock form used by helper
// tests. Production callers should use SignTailnetAssertion.
func SignTailnetAssertionAt(key []byte, claims TailnetClaims, now time.Time) (string, error) {
	if len(key) < 32 {
		return "", errors.New("tailnet assertion key must be at least 32 bytes")
	}
	if err := validateTailnetClaims(claims, now); err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", errors.New("could not encode tailnet assertion")
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	input := tailnetJWTHeader + "." + encodedPayload
	sum := hmac.New(sha256.New, key)
	_, _ = sum.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(sum.Sum(nil)), nil
}

// Verify parses and verifies one compact HS256 assertion. Request-specific
// checks are intentionally left to Manager.Authenticate so this primitive is
// useful to the loopback helper tests and cannot be misused as a full auth
// decision by itself.
func (v *TailnetJWTVerifier) Verify(_ context.Context, assertion string) (TailnetClaims, error) {
	if v == nil || len(v.key) < 32 {
		return TailnetClaims{}, errors.New("tailnet identity verifier is unavailable")
	}
	if len(assertion) == 0 || len(assertion) > TailnetMaxAssertionSize {
		return TailnetClaims{}, errors.New("invalid tailnet assertion size")
	}
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 || parts[0] != tailnetJWTHeader || parts[1] == "" || parts[2] == "" {
		return TailnetClaims{}, errors.New("invalid tailnet assertion format")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != sha256.Size {
		return TailnetClaims{}, errors.New("invalid tailnet assertion signature")
	}
	sum := hmac.New(sha256.New, v.key)
	_, _ = sum.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(signature, sum.Sum(nil)) {
		return TailnetClaims{}, errors.New("invalid tailnet assertion signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) == 0 {
		return TailnetClaims{}, errors.New("invalid tailnet assertion payload")
	}
	claims, err := decodeTailnetClaims(payload)
	if err != nil {
		return TailnetClaims{}, err
	}
	now := time.Now()
	if v.now != nil {
		now = v.now()
	}
	if err := validateTailnetClaims(claims, now); err != nil {
		return TailnetClaims{}, err
	}
	return claims, nil
}

func decodeTailnetClaims(payload []byte) (TailnetClaims, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	first, err := decoder.Token()
	if err != nil {
		return TailnetClaims{}, errors.New("invalid tailnet assertion claims")
	}
	delim, ok := first.(json.Delim)
	if !ok || delim != '{' {
		return TailnetClaims{}, errors.New("invalid tailnet assertion claims")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return TailnetClaims{}, errors.New("invalid tailnet assertion claims")
		}
		key, ok := name.(string)
		if !ok {
			return TailnetClaims{}, errors.New("invalid tailnet assertion claims")
		}
		if _, exists := fields[key]; exists {
			return TailnetClaims{}, errors.New("invalid tailnet assertion claims")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return TailnetClaims{}, errors.New("invalid tailnet assertion claims")
		}
		fields[key] = value
	}
	last, err := decoder.Token()
	if err != nil {
		return TailnetClaims{}, errors.New("invalid tailnet assertion claims")
	}
	if delim, ok := last.(json.Delim); !ok || delim != '}' {
		return TailnetClaims{}, errors.New("invalid tailnet assertion claims")
	}
	var extra any
	if trailing := decoder.Decode(&extra); !errors.Is(trailing, io.EOF) {
		return TailnetClaims{}, errors.New("invalid tailnet assertion claims")
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return TailnetClaims{}, errors.New("invalid tailnet assertion claims")
	}
	var claims TailnetClaims
	strict := json.NewDecoder(bytes.NewReader(encoded))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&claims); err != nil {
		return TailnetClaims{}, errors.New("invalid tailnet assertion claims")
	}
	return claims, nil
}

// ValidateTailnetRequest checks the signed values that must match the current
// request and configured private origin. It is exported for the HTTP layer's
// tests and for helper implementations that need the same exact predicates.
func ValidateTailnetRequest(claims TailnetClaims, issuer, audience, owner, method, requestURI string, now time.Time) error {
	return ValidateTailnetIdentity(claims, issuer, audience, owner, owner, method, requestURI, now)
}

// ValidateTailnetIdentity checks both the exact Tailnet LoginName subject and
// the configured Helm administrator email used to reconcile the existing
// human actor. Tailnet LoginName values are not required to be email-shaped.
func ValidateTailnetIdentity(claims TailnetClaims, issuer, audience, ownerLogin, adminEmail, method, requestURI string, now time.Time) error {
	if issuer == "" {
		issuer = TailnetAssertionIssuer
	}
	if err := validateTailnetClaims(claims, now); err != nil {
		return err
	}
	if claims.Issuer != issuer {
		return errors.New("tailnet assertion issuer mismatch")
	}
	if claims.Audience != audience {
		return errors.New("tailnet assertion audience mismatch")
	}
	if claims.Subject != ownerLogin {
		return errors.New("tailnet assertion owner login mismatch")
	}
	if claims.Email != adminEmail {
		return errors.New("tailnet assertion administrator mismatch")
	}
	if canonicalMethod(claims.Method) != canonicalMethod(method) {
		return errors.New("tailnet assertion method mismatch")
	}
	if claims.RequestURI != requestURI {
		return errors.New("tailnet assertion URI mismatch")
	}
	return nil
}

func validateTailnetClaims(claims TailnetClaims, now time.Time) error {
	if claims.Issuer != TailnetAssertionIssuer {
		return errors.New("invalid tailnet assertion issuer")
	}
	if !validTailnetText(claims.Audience, TailnetMaxAudienceSize) {
		return errors.New("invalid tailnet assertion audience")
	}
	if !validTailnetLogin(claims.Subject) {
		return errors.New("invalid tailnet assertion subject")
	}
	if !validEmail(claims.Email) {
		return errors.New("invalid tailnet assertion email")
	}
	if claims.Name != "" && (utf8.RuneCountInString(claims.Name) > 200 || strings.IndexFunc(claims.Name, unicode.IsControl) >= 0) {
		return errors.New("invalid tailnet assertion name")
	}
	if !validTailnetMethod(claims.Method) {
		return errors.New("invalid tailnet assertion method")
	}
	if !validTailnetRequestURI(claims.RequestURI) {
		return errors.New("invalid tailnet assertion URI")
	}
	if claims.IssuedAt <= 0 || claims.ExpiresAt <= claims.IssuedAt {
		return errors.New("invalid tailnet assertion timestamps")
	}
	nowUnix := now.Unix()
	if claims.IssuedAt > nowUnix+int64(TailnetClockSkew/time.Second) {
		return errors.New("tailnet assertion is issued in the future")
	}
	if claims.ExpiresAt <= nowUnix {
		return errors.New("tailnet assertion has expired")
	}
	if claims.ExpiresAt-claims.IssuedAt > int64(TailnetAssertionMaxTTL/time.Second) {
		return errors.New("tailnet assertion lifetime is too long")
	}
	if claims.IssuedAt < nowUnix-int64(TailnetAssertionMaxTTL/time.Second)-int64(TailnetClockSkew/time.Second) {
		return errors.New("tailnet assertion is stale")
	}
	return nil
}

func validTailnetText(value string, max int) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= max && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validTailnetLogin(value string) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 320 && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validTailnetMethod(method string) bool {
	if method == "" || len(method) > 32 || canonicalMethod(method) != method {
		return false
	}
	for _, char := range method {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '!' && char != '#' && char != '$' && char != '%' && char != '&' && char != '\'' && char != '*' && char != '+' && char != '-' && char != '.' && char != '^' && char != '_' && char != '`' && char != '|' && char != '~' {
			return false
		}
	}
	return true
}

func canonicalMethod(method string) string { return strings.ToUpper(strings.TrimSpace(method)) }

func validTailnetRequestURI(requestURI string) bool {
	if requestURI == "" || len(requestURI) > TailnetMaxURI || !utf8.ValidString(requestURI) || strings.IndexFunc(requestURI, unicode.IsControl) >= 0 || !strings.HasPrefix(requestURI, "/") || strings.HasPrefix(requestURI, "//") {
		return false
	}
	parsed, err := url.ParseRequestURI(requestURI)
	return err == nil && parsed.IsAbs() == false && parsed.Host == "" && parsed.Fragment == ""
}

// TailnetKeyFromConfig makes it harder for callers to accidentally share a
// mutable key slice with the verifier. It is intentionally small and avoids
// a second assertion-key implementation in the command helper.
func TailnetKeyFromConfig(key []byte) ([]byte, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("tailnet assertion key must be at least 32 bytes")
	}
	return append([]byte(nil), key...), nil
}
