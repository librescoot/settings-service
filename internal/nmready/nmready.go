package nmready

import (
	"context"
	"log"
	"os/exec"
	"strings"
	"time"
)

const (
	InitialBackoff = 2 * time.Second
	MaxBackoff     = 60 * time.Second
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

// Wait blocks until NetworkManager is running or ctx is cancelled. Polls with
// exponential backoff (starts at InitialBackoff, caps at MaxBackoff). There
// is no internal timeout — callers control the deadline via ctx.
func Wait(ctx context.Context) error {
	if IsRunning() {
		return nil
	}

	log.Println("Waiting for NetworkManager to become available...")

	backoff := InitialBackoff
	for {
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
			if IsRunning() {
				log.Println("NetworkManager is running")
				return nil
			}
			backoff *= 2
			if backoff > MaxBackoff {
				backoff = MaxBackoff
			}
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}
