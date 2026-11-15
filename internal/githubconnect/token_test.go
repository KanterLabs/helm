package githubconnect

import (
	"testing"
	"time"
)

func TestTokenReplyIsValidated(t *testing.T) {
	const access, refresh = `"access_token":"ghu_` + accessSentinel + `1"`, `"refresh_token":"ghr_` + refreshSentinel + `1"`
	good := map[string]tokenSet{
		`{` + access + `,"token_type":"bearer","expires_in":28800,` + refresh + `,"refresh_token_expires_in":15897600}`: {
			access: "ghu_" + accessSentinel + "1", refresh: "ghr_" + refreshSentinel + "1", lifetime: 8 * time.Hour, refreshLifetime: 15897600 * time.Second},
		`{` + access + `,"token_type":"Bearer","scope":""}`:         {access: "ghu_" + accessSentinel + "1"},
		`{` + access + `,"token_type":"bearer","expires_in":28800}`: {access: "ghu_" + accessSentinel + "1", lifetime: 8 * time.Hour},
	}
	for body, want := range good {
		got, ok := parseTokenReply([]byte(body))
		if !ok || got != want {
			t.Fatalf("%s: got %+v ok=%v", body, got, ok)
		}
	}
	bad := []string{
		`{"access_token":"ghu_` + accessSentinel + ` 1","token_type":"bearer"}`,
		`{"access_token":"ghu_` + accessSentinel + `\u00001","token_type":"bearer"}`,
		`{"access_token":"","token_type":"bearer"}`,
		`{` + access + `,"token_type":"mac"}`,
		`{` + access + `}`,
		`{` + access + `,"token_type":"bearer","expires_in":0,` + refresh + `}`,
		`{` + access + `,"token_type":"bearer",` + refresh + `}`,
		`{` + access + `,"token_type":"bearer","expires_in":10,` + refresh + `}`,
		`{` + access + `,"token_type":"bearer","expires_in":99999999,` + refresh + `}`,
		`{` + access + `,"token_type":"bearer","expires_in":-5}`,
		`{` + access + `,"token_type":"bearer","expires_in":"soon"}`,
		`{` + access + `,"token_type":"bearer","expires_in":28800,"refresh_token":"ghr_` + refreshSentinel + `\t1"}`,
		`{` + access + `,"token_type":"bearer","expires_in":28800,` + refresh + `,"refresh_token_expires_in":1}`,
		`{` + access + `,"token_type":"bearer","error":"bad_verification_code"}`,
		`{` + access,
		`[]`,
	}
	for _, body := range bad {
		if got, ok := parseTokenReply([]byte(body)); ok {
			t.Fatalf("accepted %s as %+v", body, got.lifetime)
		}
	}
}
