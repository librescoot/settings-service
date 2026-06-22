package service

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/librescoot/settings-service/internal/fileutil"
)

// OverlayStatePath persists whether the service overlay is active, separately
// from user settings, so it survives reboot (Redis is not persisted here).
var OverlayStatePath = "/data/service-mode.json"

type overlayPersisted struct {
	Active bool   `json:"active"`
	Name   string `json:"name"`
}

// loadOverlayActive reports whether a persisted overlay flag is set. A missing
// or unreadable file means inactive.
func loadOverlayActive() bool {
	data, err := os.ReadFile(OverlayStatePath)
	if err != nil {
		return false
	}
	var p overlayPersisted
	if err := json.Unmarshal(data, &p); err != nil {
		return false
	}
	return p.Active
}

// saveOverlayActive atomically writes the persisted overlay flag.
func saveOverlayActive(active bool) error {
	if err := os.MkdirAll(filepath.Dir(OverlayStatePath), 0755); err != nil {
		return err
	}
	p := overlayPersisted{Active: active, Name: "service"}
	return fileutil.AtomicWrite(OverlayStatePath, 0644, func(f *os.File) error {
		return json.NewEncoder(f).Encode(p)
	})
}
