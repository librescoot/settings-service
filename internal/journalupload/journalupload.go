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

// Both client-cert settings must be disabled together or journal-upload rejects startup.
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

	_ = exec.Command("systemctl", "stop", ServiceName).Run()
	_ = exec.Command("systemctl", "disable", ServiceName).Run()
	log.Printf("Stopped and disabled %s (log server unset)", ServiceName)
	return nil
}

func isActive() bool {
	return exec.Command("systemctl", "is-active", "--quiet", ServiceName).Run() == nil
}
