package service

import (
	"fmt"

	"github.com/librescoot/settings-service/internal/config"
)

// The saved boolean describes regular celebrations. The dashboard owns the
// separate easter-egg file, so it completes that part of the migration after
// its data partition is mounted.
func migrateMilestoneSettings(cfg *config.Config) (bool, error) {
	dashboard, ok := (*cfg)["dashboard"].(map[string]interface{})
	if !ok {
		return false, nil
	}
	old, hasOld := dashboard["milestone-celebrations"]
	if !hasOld {
		return false, nil
	}
	milestones, ok := dashboard["milestones"].(map[string]interface{})
	if !ok {
		if dashboard["milestones"] != nil {
			return false, fmt.Errorf("dashboard.milestones must be a table")
		}
		milestones = make(map[string]interface{})
		dashboard["milestones"] = milestones
	}
	if _, exists := milestones["mode"]; !exists {
		mode := "off"
		if fmt.Sprint(old) == "true" {
			mode = "regular"
			milestones["legacy-eggs-pending"] = true
		}
		milestones["mode"] = mode
	}
	delete(dashboard, "milestone-celebrations")
	return true, nil
}
