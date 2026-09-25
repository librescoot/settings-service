package service

import (
	"testing"

	"github.com/librescoot/settings-service/internal/config"
)

func TestMigrateMilestoneSettings(t *testing.T) {
	for _, tc := range []struct {
		name       string
		old        any
		newMode    string
		wantMode   string
		wantMarker any
	}{
		{"enabled", true, "", "regular", true},
		{"disabled", false, "", "off", nil},
		{"preserve explicit choice", true, "all", "all", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Dashboard: map[string]interface{}{
				"milestone-celebrations": tc.old,
			}}
			if tc.newMode != "" {
				cfg.Dashboard["milestones"] = map[string]interface{}{"mode": tc.newMode}
			}
			changed, err := migrateMilestoneSettings(cfg)
			if err != nil || !changed {
				t.Fatalf("migration = %v, %v", changed, err)
			}
			if _, oldPresent := cfg.Dashboard["milestone-celebrations"]; oldPresent {
				t.Fatal("legacy setting still present")
			}
			nested := cfg.Dashboard["milestones"].(map[string]interface{})
			if nested["mode"] != tc.wantMode || nested["legacy-eggs-pending"] != tc.wantMarker {
				t.Fatalf("milestones = %v", nested)
			}
			changed, err = migrateMilestoneSettings(cfg)
			if err != nil || changed {
				t.Fatalf("repeat migration = %v, %v", changed, err)
			}
		})
	}
}
