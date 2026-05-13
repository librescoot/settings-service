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

	"github.com/librescoot/settings-service/internal/nmready"
)

var WireGuardConfigDir = "/data/wireguard"

// Manager synchronizes the WireGuard config directory (the UMS-visible source
// of truth) with NetworkManager. New or changed *.conf files are imported;
// conf files the user removed cause the matching NM connection to be deleted.
// A sha256 sidecar per conf (<name>.sha256) records what we last imported
// so we can re-import only on actual content change.
type Manager struct {
	configDir string
}

func NewManager() *Manager {
	return &Manager{configDir: WireGuardConfigDir}
}

func NewManagerWithOptions(configDir string) *Manager {
	return &Manager{configDir: configDir}
}

// Initialize syncs /data/wireguard/ with NetworkManager. Blocks until NM is
// available (with backoff via nmready.Wait) or ctx is cancelled.
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

	// Orphan cleanup: NM has a WG connection but no matching .conf — user
	// removed the conf via UMS and expects the tunnel gone.
	for _, c := range conns {
		if _, ok := confs[c.name]; ok {
			continue
		}
		log.Printf("Removing orphaned WireGuard connection (no matching .conf): %s", c.name)
		if err := deleteByUUID(c.uuid); err != nil {
			log.Printf("Warning: delete %s (%s): %v", c.name, c.uuid, err)
		}
	}

	// Drop sidecars for confs that are gone, so a future re-add forces import.
	if err := m.pruneOrphanSidecars(confs); err != nil {
		log.Printf("Warning: prune sidecars: %v", err)
	}

	// Sync each conf.
	for name, path := range confs {
		if err := m.syncConf(name, path, conns); err != nil {
			log.Printf("Warning: sync %s: %v", name, err)
		}
	}

	log.Println("WireGuard sync completed")
	return nil
}

// syncConf imports `path` if its sha256 differs from the recorded sidecar or
// if no matching NM connection exists. Existing NM connections with the same
// name are deleted first to avoid duplicates (nmcli import always creates a
// new UUID).
func (m *Manager) syncConf(name, path string, conns []wgConn) error {
	hash, err := hashFile(path)
	if err != nil {
		return fmt.Errorf("hash conf: %w", err)
	}

	sidecar := m.sidecarPath(name)
	stored, _ := os.ReadFile(sidecar)
	nmHas := false
	for _, c := range conns {
		if c.name == name {
			nmHas = true
			break
		}
	}

	if nmHas && string(stored) == hash {
		log.Printf("WireGuard %s up to date, skipping", name)
		return nil
	}

	// Delete any NM connections with this name (could be multiple from a
	// previous duplicate-import bug). Errors are non-fatal — the import
	// step will surface a real problem.
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
	return nil
}

// listConfs returns map[name]path for every *.conf in the config dir, where
// name is the basename without the .conf suffix.
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

// pruneOrphanSidecars removes <name>.sha256 files whose .conf is gone.
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

// listWireGuardConnections returns all NM connections of type wireguard.
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
		// NAME may contain escaped colons (`\:`). Split from the right so
		// the last two fields (UUID, TYPE) are always correct.
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
