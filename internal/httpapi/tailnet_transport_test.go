package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTailnetPrivateHandlerAllowsOnlyExactEdgePeer(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler, err := NewTailnetPrivateHandler(next, []string{"10.0.0.101"})
	if err != nil {
		t.Fatal(err)
	}
	allowed := httptest.NewRequest(http.MethodGet, "https://beta-helm.home.shanekanterman.dev/", nil)
	allowed.RemoteAddr = "10.0.0.101:51900"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, allowed)
	if response.Code != http.StatusNoContent {
		t.Fatalf("allowed peer status = %d", response.Code)
	}
	for _, remote := range []string{"10.0.0.102:51900", "127.0.0.1:51900", "not-an-address"} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = remote
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("remote %q status = %d", remote, response.Code)
		}
	}
}
