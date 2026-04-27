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
	"path/filepath"
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
	CLIPath     string    `json:"cli_path,omitempty"`
	LastExit    string    `json:"last_exit,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
	ServiceName string    `json:"service_name"`
}

var (
	mu     sync.RWMutex
	status = RuntimeStatus{
		Running:     false,
		UpdatedAt:   time.Now(),
		ServiceName: "traffmonetizer",
	}
)

func setStatus(fn func(*RuntimeStatus)) {
	mu.Lock()
	defer mu.Unlock()
	fn(&status)
	status.UpdatedAt = time.Now()
}

func getStatus() RuntimeStatus {
	mu.RLock()
	defer mu.RUnlock()
	return status
}

func pathExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func findFirstExisting(paths []string) (string, bool) {
	for _, p := range paths {
		if pathExists(p) {
			return p, true
		}
	}
	return "", false
}

func findCLI() (string, error) {
	if p := os.Getenv("TM_CLI"); p != "" {
		if pathExists(p) {
			return p, nil
		}
		return "", fmt.Errorf("TM_CLI is set but not found: %s", p)
	}

	candidates := []string{
		"./cli",
		"./Cli",
		"/cli",
		"/Cli",
		"/traffmonetizer",
		"/tm",
		"/app/Cli",
		"/app/cli",
		"/usr/local/bin/cli",
		"/usr/local/bin/traffmonetizer",
		"/usr/local/bin/Cli",
		"/usr/bin/cli",
		"/usr/bin/traffmonetizer",
		"/usr/bin/Cli",
		"/entrypoint.sh",
		"/docker-entrypoint.sh",
	}

	if p, ok := findFirstExisting(candidates); ok {
		return p, nil
	}

	for _, name := range []string{"Cli", "cli", "traffmonetizer"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}

	// Some upstream images keep the executable in a non-standard location.
	for _, root := range []string{"/", "/app", "/usr/local/bin", "/usr/bin"} {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}

			name := strings.ToLower(info.Name())
			switch name {
			case "cli", "traffmonetizer", "entrypoint.sh", "docker-entrypoint.sh":
				candidates = append(candidates, path)
			}
			return nil
		})
	}

	if p, ok := findFirstExisting(candidates); ok {
		return p, nil
	}

	return "", errors.New("Traffmonetizer CLI binary not found")
}

func shellPath() (string, error) {
	if p, ok := findFirstExisting([]string{"/bin/sh", "/bin/bash"}); ok {
		return p, nil
	}
	return "", errors.New("no shell found for TM_COMMAND")
}

func buildCommand(ctx context.Context, token string) (*exec.Cmd, string, error) {
	if commandText := strings.TrimSpace(os.Getenv("TM_COMMAND")); commandText != "" {
		sh, err := shellPath()
		if err != nil {
			return nil, "", err
		}
		masked := strings.ReplaceAll(commandText, token, "****")
		return exec.CommandContext(ctx, sh, "-lc", commandText), masked, nil
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
	return exec.CommandContext(ctx, cliPath, args...), cliPath + " " + argsText + " --token ****", nil
}

func supervise(ctx context.Context, token string) {
	_, maskedCommand, err := buildCommand(ctx, token)
	if err != nil {
		log.Printf("ERROR: %v", err)
		setStatus(func(s *RuntimeStatus) {
			s.Running = false
			s.LastError = err.Error()
		})
		return
	}

	setStatus(func(s *RuntimeStatus) {
		s.CLIPath = maskedCommand
	})

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		cmd, maskedCommand, err := buildCommand(ctx, token)
		if err != nil {
			log.Printf("failed to build Traffmonetizer command: %v", err)
			setStatus(func(s *RuntimeStatus) {
				s.Running = false
				s.PID = 0
				s.LastError = err.Error()
				s.Restarts++
			})
			time.Sleep(10 * time.Second)
			continue
		}

		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		log.Printf("starting Traffmonetizer: %s", maskedCommand)

		if err := cmd.Start(); err != nil {
			log.Printf("failed to start Traffmonetizer: %v", err)
			setStatus(func(s *RuntimeStatus) {
				s.Running = false
				s.PID = 0
				s.LastError = err.Error()
				s.Restarts++
			})
			time.Sleep(10 * time.Second)
			continue
		}

		setStatus(func(s *RuntimeStatus) {
			s.Running = true
			s.PID = cmd.Process.Pid
			s.StartedAt = time.Now()
			s.LastError = ""
			s.LastExit = ""
		})

		err := cmd.Wait()

		if ctx.Err() != nil {
			return
		}

		exitMsg := "exited"
		if err != nil {
			exitMsg = err.Error()
		}

		log.Printf("Traffmonetizer exited: %s", exitMsg)

		setStatus(func(s *RuntimeStatus) {
			s.Running = false
			s.PID = 0
			s.LastExit = exitMsg
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
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	token := os.Getenv("TM_TOKEN")
	if token == "" {
		log.Println("WARNING: TM_TOKEN is empty; Traffmonetizer will not start")
		setStatus(func(s *RuntimeStatus) {
			s.Running = false
			s.LastError = "missing TM_TOKEN environment variable"
		})
	} else {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		go supervise(ctx, token)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s := getStatus()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      s.Running,
			"message": "Traffmonetizer monitor is running",
			"status":  s,
		})
	})

	http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, getStatus())
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		s := getStatus()
		if s.Running {
			writeJSON(w, http.StatusOK, s)
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, s)
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

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
