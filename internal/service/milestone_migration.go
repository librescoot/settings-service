package service

import (
	"fmt"

	"github.com/librescoot/settings-service/internal/config"
)

// The saved boolean describes regular celebrations. The dashboard owns the
// separate easter-egg file, so it completes that part of the migration after
// its data partition is mounted.
func migrateMilestoneSettings(cfg *config.Config) (bool, error) {
	if cfg.Dashboard == nil {
		return false, nil
	}
	old, hasOld := cfg.Dashboard["milestone-celebrations"]
	if !hasOld {
		return false, nil
	}
	milestones, ok := cfg.Dashboard["milestones"].(map[string]interface{})
	if !ok {
		if cfg.Dashboard["milestones"] != nil {
			return false, fmt.Errorf("dashboard.milestones must be a table")
		}
		milestones = make(map[string]interface{})
		cfg.Dashboard["milestones"] = milestones
	}
	if _, exists := milestones["mode"]; !exists {
		mode := "off"
		if fmt.Sprint(old) == "true" {
			mode = "regular"
			milestones["legacy-eggs-pending"] = true
		}
		milestones["mode"] = mode
	}
	delete(cfg.Dashboard, "milestone-celebrations")
	return true, nil
}
