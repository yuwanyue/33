package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var (
	procMu   sync.RWMutex
	xrayProc *os.Process
)

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func findTmCLI() string {
	candidates := []string{
		"/Cli", "/cli", "/tm", "/traffmonetizer", "/app/Cli", "/app/cli",
		"/usr/local/bin/Cli", "/usr/local/bin/cli", "/usr/local/bin/traffmonetizer",
		"/usr/bin/Cli", "/usr/bin/cli", "/usr/bin/traffmonetizer",
		"/tmroot/Cli", "/tmroot/cli", "/tmroot/tm", "/tmroot/traffmonetizer",
		"/tmroot/app/Cli", "/tmroot/app/cli",
		"/tmroot/usr/local/bin/Cli", "/tmroot/usr/local/bin/cli", "/tmroot/usr/local/bin/traffmonetizer",
		"/tmroot/usr/bin/Cli", "/tmroot/usr/bin/cli", "/tmroot/usr/bin/traffmonetizer",
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	for _, n := range []string{"Cli", "cli", "traffmonetizer"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

func startTraffmonetizer() {
	token := strings.TrimSpace(os.Getenv("TM_TOKEN"))
	if token == "" {
		log.Printf("TM_TOKEN empty, skip Traffmonetizer")
		return
	}

	tm := findTmCLI()
	if tm == "" {
		log.Printf("Traffmonetizer binary not found, skip")
		return
	}

	argsText := getenv("TM_ARGS", "start accept")
	args := append(strings.Fields(argsText), "--token", token)
	cmd := exec.Command(tm, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("start Traffmonetizer failed: %v", err)
		return
	}
	log.Printf("Traffmonetizer started pid=%d", cmd.Process.Pid)

	go func() {
		err := cmd.Wait()
		if err != nil {
			log.Printf("Traffmonetizer exited: %v", err)
		} else {
			log.Printf("Traffmonetizer exited")
		}
	}()
}

func writeXrayConfig(path, port, uuid, wsPath string) error {
	config := fmt.Sprintf(`{
  "log": {
    "loglevel": "warning"
  },
  "inbounds": [
    {
      "listen": "0.0.0.0",
      "port": %s,
      "protocol": "vless",
      "settings": {
        "clients": [
          {
            "id": %q,
            "flow": ""
          }
        ],
        "decryption": "none"
      },
      "streamSettings": {
        "network": "ws",
        "security": "none",
        "wsSettings": {
          "path": %q
        }
      }
    }
  ],
  "outbounds": [
    {
      "protocol": "freedom",
      "tag": "direct"
    },
    {
      "protocol": "blackhole",
      "tag": "blocked"
    }
  ]
}
`, port, uuid, wsPath)
	return os.WriteFile(path, []byte(config), 0o644)
}

func startXray(xrayPort, uuid, wsPath string) error {
	xrayPath, err := exec.LookPath("xray")
	if err != nil {
		xrayPath = "/usr/local/bin/xray"
	}

	cfg := filepath.Join(os.TempDir(), "xray-config.json")
	if err := writeXrayConfig(cfg, xrayPort, uuid, wsPath); err != nil {
		return err
	}

	cmd := exec.Command(xrayPath, "run", "-config", cfg)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	procMu.Lock()
	xrayProc = cmd.Process
	procMu.Unlock()

	log.Printf("Xray started pid=%d port=%s path=%s", cmd.Process.Pid, xrayPort, wsPath)
	go func() {
		err := cmd.Wait()
		procMu.Lock()
		xrayProc = nil
		procMu.Unlock()
		if err != nil {
			log.Printf("Xray exited: %v", err)
		} else {
			log.Printf("Xray exited")
		}
	}()
	return nil
}

func xrayRunning() bool {
	procMu.RLock()
	defer procMu.RUnlock()
	return xrayProc != nil
}

func main() {
	listenPort := getenv("PORT", "8080")
	xrayPort := getenv("XRAY_PORT", "10000")
	wsPath := getenv("VLESS_WS_PATH", "/ws")
	uuid := getenv("VLESS_UUID", "10974d1a-cbd6-4b6f-db1d-38d78b3fb109")

	if err := startXray(xrayPort, uuid, wsPath); err != nil {
		log.Fatalf("failed to start xray: %v", err)
	}
	startTraffmonetizer()

	upstreamURL, err := url.Parse("http://127.0.0.1:" + xrayPort)
	if err != nil {
		log.Fatalf("invalid upstream: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstreamURL)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		log.Printf("proxy error: %v", e)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	http.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if xrayRunning() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("xray down"))
	})
	http.HandleFunc(wsPath, func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("alive"))
	})

	addr := ":" + listenPort
	log.Printf("edge listening on %s, ws=%s -> 127.0.0.1:%s", addr, wsPath, xrayPort)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
