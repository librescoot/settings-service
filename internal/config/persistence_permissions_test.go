package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSavePreservesSettingsPermissions(t *testing.T) {
	previous := TomlFilePath
	t.Cleanup(func() { TomlFilePath = previous })
	TomlFilePath = filepath.Join(t.TempDir(), "settings.toml")
	cfg := Config{"cellular": map[string]interface{}{"apn": "example.apn"}}
	for _, mode := range []os.FileMode{0600, 0640} {
		if err := os.WriteFile(TomlFilePath, []byte("[cellular]\napn=\"old.apn\"\n"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(TomlFilePath, mode); err != nil {
			t.Fatal(err)
		}
		if err := SaveToFile(&cfg); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(TomlFilePath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("permissions changed: got %o, want %o", info.Mode().Perm(), mode)
		}
	}
}

func TestSaveRefusesToReplaceSettingsSymlink(t *testing.T) {
	previous := TomlFilePath
	t.Cleanup(func() { TomlFilePath = previous })
	dir := t.TempDir()
	TomlFilePath = filepath.Join(dir, "settings.toml")
	target := filepath.Join(dir, "other.toml")
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, TomlFilePath); err != nil {
		t.Fatal(err)
	}
	cfg := Config{"cellular": map[string]interface{}{"apn": "example.apn"}}
	if err := SaveToFile(&cfg); err == nil {
		t.Fatal("settings symlink was replaced")
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "unchanged" {
		t.Fatal("symlink target changed")
	}
	if _, err := os.Readlink(TomlFilePath); err != nil {
		t.Fatal("settings symlink changed")
	}
}
