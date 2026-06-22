package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOverlayActiveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	OverlayStatePath = filepath.Join(dir, "service-mode.json")

	if loadOverlayActive() {
		t.Fatal("expected inactive when file absent")
	}
	if err := saveOverlayActive(true); err != nil {
		t.Fatalf("saveOverlayActive: %v", err)
	}
	if !loadOverlayActive() {
		t.Error("expected active after save(true)")
	}
	if err := saveOverlayActive(false); err != nil {
		t.Fatalf("saveOverlayActive: %v", err)
	}
	if loadOverlayActive() {
		t.Error("expected inactive after save(false)")
	}
	if _, err := os.Stat(OverlayStatePath); err != nil {
		t.Errorf("state file should exist: %v", err)
	}
}
