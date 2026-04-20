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
//	""               -> stop + disable the service
//	config changed   -> write config, enable + restart
//	config unchanged -> start if not running, otherwise no-op
//
// The generated config sets ServerKeyFile=-, ServerCertificateFile=-,
// and TrustedCertificateFile=- so plain http:// URLs work and https://
// URLs don't fail because journal-upload can't find the default
// client-cert/private-key pair at /etc/ssl/{private,certs}/journal-
// upload.pem. Both key and cert must be set together or journal-upload
// refuses to start ("Options --key= and --cert= must be used together.").
func ApplyLogServer(desired string) error {
	desired = strings.TrimSpace(desired)

	if desired == "" {
		return stopAndDisable()
	}

	expected := buildConfig(desired)

	current, err := os.ReadFile(ConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", ConfigPath, err)
	}

	if string(current) != expected {
		currentURL, _ := GetCurrentLogServer()
		log.Printf("journal-upload config updating (url %q -> %q)", currentURL, desired)
		if err := writeConfigContent(expected); err != nil {
			return fmt.Errorf("write %s: %w", ConfigPath, err)
		}
		return enableAndRestart()
	}

	if isActive() {
		return nil
	}
	log.Printf("journal-upload config unchanged (%s) but service inactive; starting", desired)
	return enableAndRestart()
}

// buildConfig returns the canonical journal-upload.conf content for a URL.
// ServerKeyFile=- + ServerCertificateFile=- together tell journal-upload
// to skip loading any client certificate / private key (journal-upload
// rejects startup if only one of the two is set). The compiled-in default
// paths under /etc/ssl/{private,certs}/journal-upload.pem don't exist on
// the scooter. TrustedCertificateFile=- disables server-cert verification,
// letting self-signed or unknown-CA https:// endpoints work without a CA
// bundle.
func buildConfig(url string) string {
	return fmt.Sprintf("[Upload]\nURL=%s\nServerKeyFile=-\nServerCertificateFile=-\nTrustedCertificateFile=-\n", url)
}

func writeConfigContent(content string) error {
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
