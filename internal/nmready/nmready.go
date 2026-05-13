package nmready

import (
	"context"
	"errors"
	"log"
	"os/exec"
	"strings"
	"time"
)

const (
	DefaultTimeout = 120 * time.Second
	PollInterval   = 2 * time.Second
)

// IsRunning returns true if NetworkManager currently responds as running.
func IsRunning() bool {
	cmd := exec.Command("nmcli", "-t", "-f", "RUNNING", "general")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "running"
}

// Wait blocks until NetworkManager is running, ctx is cancelled, or timeout
// elapses. Returns nil on success, ctx.Err() on cancel, or a timeout error.
func Wait(ctx context.Context, timeout time.Duration) error {
	if IsRunning() {
		return nil
	}

	log.Printf("Waiting for NetworkManager to become available (timeout %v)...", timeout)

	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-ticker.C:
			if IsRunning() {
				log.Println("NetworkManager is running")
				return nil
			}
		case <-timer.C:
			return errors.New("timed out waiting for NetworkManager")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
