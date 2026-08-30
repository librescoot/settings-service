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

func IsRunning() bool {
	cmd := exec.Command("nmcli", "-t", "-f", "RUNNING", "general")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "running"
}

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
