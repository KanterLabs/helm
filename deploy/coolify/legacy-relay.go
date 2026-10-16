// Retain the existing bearer API tunnel without retaining an old database writer.
package main

import (
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

func main() {
	upstream, _ := url.Parse("https://10.0.0.101:19612")
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	proxy.Transport = &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSClientConfig:       &tls.Config{ServerName: "helm.home.shanekanterman.dev", MinVersion: tls.VersionTLS12},
		ResponseHeaderTimeout: 30 * time.Second,
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			http.NotFound(w, r)
			return
		}
		r.Header.Del("Cookie")
		r.Host = "tc.shanekanterman.dev"
		proxy.ServeHTTP(w, r)
	})
	server := &http.Server{Addr: "127.0.0.1:8080", Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	log.Fatal(server.ListenAndServe())
}
