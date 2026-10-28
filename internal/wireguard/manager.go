package wireguard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/librescoot/settings-service/internal/nmready"
)

var WireGuardConfigDir = "/data/wireguard"

type Manager struct {
	configDir    string
	activate     func(context.Context, string) error
	initialRetry time.Duration
	maximumRetry time.Duration
}

func NewManager() *Manager {
	return newManager(WireGuardConfigDir)
}

func NewManagerWithOptions(configDir string) *Manager {
	return newManager(configDir)
}

func newManager(configDir string) *Manager {
	return &Manager{
		configDir:    configDir,
		activate:     activateConnection,
		initialRetry: 2 * time.Second,
		maximumRetry: 60 * time.Second,
	}
}

func (m *Manager) Initialize(ctx context.Context) error {
	log.Println("Starting WireGuard sync...")

	if err := nmready.Wait(ctx); err != nil {
		return fmt.Errorf("wait for NetworkManager: %w", err)
	}

	confs, err := m.listConfs()
	if err != nil {
		return fmt.Errorf("list configs: %w", err)
	}

	conns, err := listWireGuardConnections()
	if err != nil {
		return fmt.Errorf("list NM wireguard connections: %w", err)
	}

	for _, c := range conns {
		if _, ok := confs[c.name]; ok {
			continue
		}
		log.Printf("Removing orphaned WireGuard connection (no matching .conf): %s", c.name)
		if err := deleteByUUID(c.uuid); err != nil {
			log.Printf("Warning: delete %s (%s): %v", c.name, c.uuid, err)
		}
	}

	if err := m.pruneOrphanSidecars(confs); err != nil {
		log.Printf("Warning: prune sidecars: %v", err)
	}

	var profiles []string
	for name, path := range confs {
		if err := m.syncConf(name, path, conns); err != nil {
			log.Printf("Warning: sync %s: %v", name, err)
			continue
		}
		profiles = append(profiles, name)
	}

	log.Println("WireGuard sync completed")
	return m.activateProfiles(ctx, profiles)
}

func (m *Manager) activateProfiles(ctx context.Context, profiles []string) error {
	pending := make(map[string]struct{}, len(profiles))
	for _, name := range profiles {
		pending[name] = struct{}{}
	}

	retry := m.initialRetry
	for len(pending) > 0 {
		for name := range pending {
			if err := m.activate(ctx, name); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				log.Printf("WireGuard %s activation failed, retrying: %v", name, err)
				continue
			}
			log.Printf("WireGuard %s activated", name)
			delete(pending, name)
		}
		if len(pending) == 0 {
			return nil
		}

		timer := time.NewTimer(retry)
		select {
		case <-timer.C:
			if retry < m.maximumRetry {
				retry *= 2
				if retry > m.maximumRetry {
					retry = m.maximumRetry
				}
			}
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
	return nil
}

func activateConnection(ctx context.Context, name string) error {
	out, err := exec.CommandContext(ctx, "nmcli", "--wait", "20", "connection", "up", "id", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("nmcli connection up: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *Manager) syncConf(name, path string, conns []wgConn) error {
	hash, err := hashFile(path)
	if err != nil {
		return fmt.Errorf("hash conf: %w", err)
	}

	sidecar := m.sidecarPath(name)
	storedRaw, _ := os.ReadFile(sidecar)
	stored := strings.TrimSpace(string(storedRaw))
	nmHas := false
	for _, c := range conns {
		if c.name == name {
			nmHas = true
			break
		}
	}

	if nmHas && stored == hash {
		log.Printf("WireGuard %s up to date, skipping", name)
		if err := ensureAutoconnect(name); err != nil {
			log.Printf("Warning: enforce autoconnect on %s: %v", name, err)
		}
		return nil
	}

	for _, c := range conns {
		if c.name != name {
			continue
		}
		if err := deleteByUUID(c.uuid); err != nil {
			log.Printf("Warning: delete %s (%s): %v", name, c.uuid, err)
		}
	}

	log.Printf("Importing WireGuard configuration: %s", filepath.Base(path))
	out, err := exec.Command("nmcli", "connection", "import", "type", "wireguard", "file", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("nmcli import: %w: %s", err, strings.TrimSpace(string(out)))
	}

	if err := os.WriteFile(sidecar, []byte(hash), 0600); err != nil {
		log.Printf("Warning: write sidecar %s: %v", sidecar, err)
	}
	if err := ensureAutoconnect(name); err != nil {
		log.Printf("Warning: enforce autoconnect on %s: %v", name, err)
	}
	return nil
}

// NetworkManager's default retry limit can leave late-boot tunnels offline.
func ensureAutoconnect(name string) error {
	out, err := exec.Command("nmcli", "con", "modify", name,
		"connection.autoconnect", "yes",
		"connection.autoconnect-retries", "0").CombinedOutput()
	if err != nil {
		return fmt.Errorf("nmcli modify: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *Manager) listConfs() (map[string]string, error) {
	out := map[string]string{}
	if _, err := os.Stat(m.configDir); os.IsNotExist(err) {
		return out, nil
	}
	matches, err := filepath.Glob(filepath.Join(m.configDir, "*.conf"))
	if err != nil {
		return nil, err
	}
	for _, p := range matches {
		name := strings.TrimSuffix(filepath.Base(p), ".conf")
		out[name] = p
	}
	return out, nil
}

func (m *Manager) sidecarPath(name string) string {
	return filepath.Join(m.configDir, name+".sha256")
}

func (m *Manager) pruneOrphanSidecars(confs map[string]string) error {
	matches, err := filepath.Glob(filepath.Join(m.configDir, "*.sha256"))
	if err != nil {
		return err
	}
	for _, p := range matches {
		name := strings.TrimSuffix(filepath.Base(p), ".sha256")
		if _, ok := confs[name]; ok {
			continue
		}
		if err := os.Remove(p); err != nil {
			log.Printf("Warning: remove stale sidecar %s: %v", p, err)
		}
	}
	return nil
}

type wgConn struct {
	name string
	uuid string
}

func listWireGuardConnections() ([]wgConn, error) {
	out, err := exec.Command("nmcli", "-t", "-f", "NAME,UUID,TYPE", "con", "show").Output()
	if err != nil {
		return nil, fmt.Errorf("nmcli con show: %w", err)
	}
	var conns []wgConn
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Names may contain escaped colons; UUID and type are the final fields.
		parts := strings.Split(line, ":")
		if len(parts) < 3 {
			continue
		}
		typ := parts[len(parts)-1]
		uuid := parts[len(parts)-2]
		name := strings.Join(parts[:len(parts)-2], ":")
		name = strings.ReplaceAll(name, `\:`, ":")
		if typ != "wireguard" {
			continue
		}
		conns = append(conns, wgConn{name: name, uuid: uuid})
	}
	return conns, nil
}

func deleteByUUID(uuid string) error {
	return exec.Command("nmcli", "con", "delete", uuid).Run()
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
