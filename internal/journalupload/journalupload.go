package journalupload

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/librescoot/settings-service/internal/fileutil"
)

const (
	ConfigPath  = "/etc/systemd/journal-upload.conf"
	ServiceName = "systemd-journal-upload.service"
)

// GetCurrentLogServer reads URL= from the [Upload] section of the
// journal-upload config. Returns "" if the file doesn't exist or the URL
// isn't set.
func GetCurrentLogServer() (string, error) {
	f, err := os.Open(ConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to open %s: %w", ConfigPath, err)
	}
	defer f.Close()

	inUpload := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inUpload = line == "[Upload]"
			continue
		}
		if inUpload && strings.HasPrefix(line, "URL=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "URL=")), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to read %s: %w", ConfigPath, err)
	}
	return "", nil
}

// ApplyLogServer reconciles the journal-upload config and service state
// with the desired URL.
//
//	""         -> stop + disable the service
//	new URL    -> write config, enable + restart
//	same URL   -> start if not running, otherwise no-op
//
// The generated config sets TrustedCertificateFile=- so plain http:// URLs
// work and any https:// URL with an unknown CA won't fail verification.
func ApplyLogServer(desired string) error {
	desired = strings.TrimSpace(desired)

	if desired == "" {
		return stopAndDisable()
	}

	current, err := GetCurrentLogServer()
	if err != nil {
		return err
	}

	if current != desired {
		log.Printf("journal-upload log server changing: %q -> %q", current, desired)
		if err := writeConfig(desired); err != nil {
			return fmt.Errorf("write %s: %w", ConfigPath, err)
		}
		return enableAndRestart()
	}

	if isActive() {
		return nil
	}
	log.Printf("journal-upload log server already configured (%s) but service inactive; starting", desired)
	return enableAndRestart()
}

func writeConfig(url string) error {
	// TrustedCertificateFile=- disables TLS certificate verification. It is a
	// no-op for http:// URLs and lets https:// URLs work without a CA
	// bundle. We do not write ServerKeyFile/ServerCertificateFile because we
	// do not use client-cert auth.
	content := fmt.Sprintf("[Upload]\nURL=%s\nTrustedCertificateFile=-\n", url)

	return fileutil.AtomicWrite(ConfigPath, 0644, func(f *os.File) error {
		_, err := f.WriteString(content)
		return err
	})
}

func enableAndRestart() error {
	if err := exec.Command("systemctl", "enable", ServiceName).Run(); err != nil {
		return fmt.Errorf("enable %s: %w", ServiceName, err)
	}
	if err := exec.Command("systemctl", "restart", ServiceName).Run(); err != nil {
		return fmt.Errorf("restart %s: %w", ServiceName, err)
	}
	log.Printf("Enabled and restarted %s", ServiceName)
	return nil
}

func stopAndDisable() error {
	// Ignore errors — the service may already be stopped/disabled.
	_ = exec.Command("systemctl", "stop", ServiceName).Run()
	_ = exec.Command("systemctl", "disable", ServiceName).Run()
	log.Printf("Stopped and disabled %s (log server unset)", ServiceName)
	return nil
}

func isActive() bool {
	return exec.Command("systemctl", "is-active", "--quiet", ServiceName).Run() == nil
}
