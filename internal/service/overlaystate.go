package service

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/librescoot/settings-service/internal/fileutil"
)

var OverlayStatePath = "/data/service-mode.json"

type overlayPersisted struct {
	Active bool   `json:"active"`
	Name   string `json:"name"`
}

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

func saveOverlayActive(active bool) error {
	if err := os.MkdirAll(filepath.Dir(OverlayStatePath), 0755); err != nil {
		return err
	}
	p := overlayPersisted{Active: active, Name: "service"}
	return fileutil.AtomicWrite(OverlayStatePath, 0644, func(f *os.File) error {
		return json.NewEncoder(f).Encode(p)
	})
}
