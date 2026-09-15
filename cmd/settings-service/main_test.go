package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestStartupHydrationFailureDoesNotNotifyOrReplaceRetentionPolicy(t *testing.T) {
	server := miniredis.RunT(t)
	server.HSet("settings", "trip.expunge", "never")
	server.HSet("settings", "trip.retention-sentinel", "preserve-history")

	tempDir := t.TempDir()
	settingsPath := filepath.Join(tempDir, "settings.toml")
	if err := os.WriteFile(settingsPath, []byte("[trip\nexpunge = \"age:365d\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	notifyPath := filepath.Join(tempDir, "notify.sock")
	notifyConn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: notifyPath, Net: "unixgram"})
	if err != nil {
		t.Fatalf("ListenUnixgram() error: %v", err)
	}
	defer notifyConn.Close()

	binaryPath := filepath.Join(tempDir, "settings-service")
	build := exec.Command("go", "build", "-o", binaryPath, ".")
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build error: %v\n%s", err, output)
	}

	schemaPath := filepath.Join("..", "..", "settings.schema.json")
	command := exec.Command(binaryPath, "--settings-file", settingsPath, "--schema", schemaPath)
	command.Env = append(os.Environ(), "REDIS_ADDR="+server.Addr(), "NOTIFY_SOCKET="+notifyPath)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("settings-service unexpectedly started successfully:\n%s", output)
	}

	buffer := make([]byte, 64)
	if err := notifyConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := notifyConn.ReadFromUnix(buffer); err == nil {
		t.Fatal("settings-service sent readiness after hydration failure")
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("read readiness socket: %v", err)
	}

	if got := server.HGet("settings", "trip.expunge"); got != "never" {
		t.Errorf("trip.expunge = %q, want existing safe policy never", got)
	}
	if got := server.HGet("settings", "trip.retention-sentinel"); got != "preserve-history" {
		t.Errorf("retention sentinel = %q, want existing value", got)
	}
}
