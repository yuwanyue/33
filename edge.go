package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func main() {
	listenPort := getenv("PORT", "8080")
	wsPath := getenv("VLESS_WS_PATH", "/ws")
	upstreamAddr := getenv("XRAY_UPSTREAM", "127.0.0.1:10000")

	upstreamURL, err := url.Parse("http://" + upstreamAddr)
	if err != nil {
		log.Fatalf("invalid upstream: %v", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(upstreamURL)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		log.Printf("proxy error: %v", e)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}

	checkUpstream := func() error {
		conn, err := net.DialTimeout("tcp", upstreamAddr, 1200*time.Millisecond)
		if err != nil {
			return err
		}
		_ = conn.Close()
		return nil
	}

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := checkUpstream(); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	http.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := checkUpstream(); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})

	http.HandleFunc(wsPath, func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("alive"))
	})

	addr := ":" + listenPort
	log.Printf("edge listening on %s, ws path=%s -> %s", addr, wsPath, upstreamAddr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(fmt.Errorf("edge server failed: %w", err))
	}
}
