package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

type RuntimeStatus struct {
	Running     bool      `json:"running"`
	PID         int       `json:"pid,omitempty"`
	Restarts    int       `json:"restarts"`
	Command     string    `json:"command,omitempty"`
	LastExit    string    `json:"last_exit,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
	ServiceName string    `json:"service_name"`
}

var (
	statusMu sync.RWMutex
	status   = RuntimeStatus{
		UpdatedAt:   time.Now(),
		ServiceName: "traffmonetizer",
	}
)

func setStatus(update func(*RuntimeStatus)) {
	statusMu.Lock()
	defer statusMu.Unlock()
	update(&status)
	status.UpdatedAt = time.Now()
}

func getStatus() RuntimeStatus {
	statusMu.RLock()
	defer statusMu.RUnlock()
	return status
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func findShell() (string, error) {
	for _, candidate := range []string{"/bin/sh", "/bin/bash"} {
		if fileExists(candidate) {
			return candidate, nil
		}
	}
	return "", errors.New("no shell found")
}

func findCLI() (string, error) {
	if explicit := strings.TrimSpace(os.Getenv("TM_CLI")); explicit != "" {
		if fileExists(explicit) {
			return explicit, nil
		}
		return "", fmt.Errorf("TM_CLI is set but not found: %s", explicit)
	}

	candidates := []string{
		"/Cli",
		"/cli",
		"/tm",
		"/traffmonetizer",
		"/app/Cli",
		"/app/cli",
		"/usr/local/bin/Cli",
		"/usr/local/bin/cli",
		"/usr/local/bin/traffmonetizer",
		"/usr/bin/Cli",
		"/usr/bin/cli",
		"/usr/bin/traffmonetizer",
	}

	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate, nil
		}
	}

	for _, candidate := range []string{"Cli", "cli", "traffmonetizer"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}

	return "", errors.New("Traffmonetizer CLI binary not found")
}

func buildCommand(ctx context.Context, token string) (*exec.Cmd, string, error) {
	if raw := strings.TrimSpace(os.Getenv("TM_COMMAND")); raw != "" {
		shellPath, err := findShell()
		if err != nil {
			return nil, "", err
		}
		masked := strings.ReplaceAll(raw, token, "****")
		return exec.CommandContext(ctx, shellPath, "-lc", raw), masked, nil
	}

	cliPath, err := findCLI()
	if err != nil {
		return nil, "", err
	}

	argsText := strings.TrimSpace(os.Getenv("TM_ARGS"))
	if argsText == "" {
		argsText = "start accept"
	}

	args := append(strings.Fields(argsText), "--token", token)
	masked := cliPath + " " + argsText + " --token ****"
	return exec.CommandContext(ctx, cliPath, args...), masked, nil
}

func supervise(ctx context.Context, token string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		cmd, masked, err := buildCommand(ctx, token)
		if err != nil {
			log.Printf("failed to build command: %v", err)
			setStatus(func(s *RuntimeStatus) {
				s.Running = false
				s.PID = 0
				s.Command = ""
				s.LastError = err.Error()
				s.Restarts++
			})
			time.Sleep(10 * time.Second)
			continue
		}

		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		log.Printf("starting Traffmonetizer: %s", masked)

		if err := cmd.Start(); err != nil {
			log.Printf("failed to start Traffmonetizer: %v", err)
			setStatus(func(s *RuntimeStatus) {
				s.Running = false
				s.PID = 0
				s.Command = masked
				s.LastError = err.Error()
				s.Restarts++
			})
			time.Sleep(10 * time.Second)
			continue
		}

		setStatus(func(s *RuntimeStatus) {
			s.Running = true
			s.PID = cmd.Process.Pid
			s.Command = masked
			s.StartedAt = time.Now()
			s.LastError = ""
			s.LastExit = ""
		})

		waitErr := cmd.Wait()
		if ctx.Err() != nil {
			return
		}

		exitMessage := "exited"
		if waitErr != nil {
			exitMessage = waitErr.Error()
		}

		log.Printf("Traffmonetizer exited: %s", exitMessage)

		setStatus(func(s *RuntimeStatus) {
			s.Running = false
			s.PID = 0
			s.LastExit = exitMessage
			s.Restarts++
		})

		time.Sleep(10 * time.Second)
	}
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func main() {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}

	token := strings.TrimSpace(os.Getenv("TM_TOKEN"))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if token == "" {
		log.Println("TM_TOKEN is empty; monitor will stay up but Traffmonetizer will not start")
		setStatus(func(s *RuntimeStatus) {
			s.Running = false
			s.LastError = "missing TM_TOKEN environment variable"
		})
	} else {
		go supervise(ctx, token)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s := getStatus()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"service": "tm-monitor",
			"status":  s,
		})
	})

	http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, getStatus())
	})

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"service": "tm-monitor",
			"status":  getStatus(),
		})
	})

	http.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		s := getStatus()
		if s.Running {
			writeJSON(w, http.StatusOK, s)
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, s)
	})

	addr := "0.0.0.0:" + port
	log.Printf("HTTP monitor listening on %s", addr)

	server := &http.Server{Addr: addr}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
