//go:build unix

package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAppServerSession_CloseKillsProcessGroup(t *testing.T) {
	workDir := t.TempDir()
	binDir := filepath.Join(workDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}

	childPIDFile := filepath.Join(workDir, "child.pid")
	script := `#!/bin/sh
(sleep 30) &
printf '%s\n' "$!" > "$CHILD_PID_FILE"
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"test"}}'
      ;;
    *'"method":"thread/start"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"cwd":"/tmp","model":"test-model","reasoningEffort":"xhigh","thread":{"id":"thread-close"}}}'
      ;;
    *'"method":"account/rateLimits/read"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":3,"result":{"rateLimits":{}}}'
      ;;
  esac
done
wait
`
	scriptPath := filepath.Join(binDir, "codex")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	t.Setenv("CHILD_PID_FILE", childPIDFile)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	s, err := newAppServerSession(context.Background(), "stdio://", workDir, "", "", "", "", "", "", nil, "")
	if err != nil {
		t.Fatalf("newAppServerSession: %v", err)
	}

	childPID := waitForPIDFile(t, childPIDFile)
	t.Cleanup(func() {
		if processExists(childPID) {
			_ = syscall.Kill(childPID, syscall.SIGKILL)
		}
	})

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !processExists(childPID) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("child process %d still alive after app-server Close", childPID)
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr != nil {
				t.Fatalf("parse pid file: %v", parseErr)
			}
			return pid
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for pid file %s: %v", path, lastErr)
	return 0
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
