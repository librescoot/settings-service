package service

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/librescoot/settings-service/internal/config"
	"github.com/librescoot/settings-service/internal/fileutil"
)

func migrateAlarmCooldown(cfg *config.Config) error {
	marker := config.TomlFilePath + ".alarm-cooldown-v1"
	if _, err := os.Stat(marker); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect alarm cooldown migration: %w", err)
	}

	if cfg != nil {
		if alarm, ok := (*cfg)["alarm"].(map[string]interface{}); ok && fmt.Sprint(alarm["l1-cooldown"]) == "15" {
			alarm["l1-cooldown"] = 2
			if err := config.SaveToFile(cfg); err != nil {
				return fmt.Errorf("save alarm cooldown migration: %w", err)
			}
		}
	}

	// Save the setting first; the marker preserves later explicit choices of 15.
	if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
		return fmt.Errorf("create alarm migration directory: %w", err)
	}
	return fileutil.AtomicWrite(marker, 0600, func(f *os.File) error {
		_, err := f.WriteString("complete\n")
		return err
	})
}
