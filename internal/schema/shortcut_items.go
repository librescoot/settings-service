package schema

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const ShortcutItemsFormat = "shortcut-items"

// maxShortcutItems bounds the list: seven fixed actions plus the thirty
// saved-location slots, with slack, so a malformed file cannot flood the menu.
const maxShortcutItems = 64

// shortcutActionIDs mirrors the action kinds the dashboard can execute.
// Register a new id here when adding a shortcut action.
var shortcutActionIDs = map[string]bool{
	"view":            true,
	"theme":           true,
	"debug-overlay":   true,
	"motion-debug":    true,
	"route-overview":  true,
	"skip-stop":       true,
	"stop-navigation": true,
}

var shortcutDestinationIcons = map[string]bool{
	"home":     true,
	"work":     true,
	"favorite": true,
	"place":    true,
}

var canonicalUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidateShortcutItems validates the dashboard.shortcut-menu.items wire
// value: a JSON array of action ids and destination tokens with no duplicate
// action identity.
func ValidateShortcutItems(value string) error {
	var items []string
	if err := json.Unmarshal([]byte(value), &items); err != nil {
		return fmt.Errorf("shortcut items must be a JSON array of strings: %w", err)
	}
	if len(items) > maxShortcutItems {
		return fmt.Errorf("shortcut items has %d entries, max is %d", len(items), maxShortcutItems)
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		identity, err := shortcutItemIdentity(item)
		if err != nil {
			return err
		}
		if seen[identity] {
			return fmt.Errorf("duplicate shortcut item %q", identity)
		}
		seen[identity] = true
	}
	return nil
}

// shortcutItemIdentity returns the stable identity a menu item is deduplicated
// on: the action id itself, or the destination uuid regardless of icon.
func shortcutItemIdentity(item string) (string, error) {
	if item == "" {
		return "", fmt.Errorf("empty shortcut item")
	}
	if rest, found := strings.CutPrefix(item, "destination:"); found {
		uuid, icon, found := strings.Cut(rest, ":")
		if !found || icon == "" || strings.Contains(icon, ":") {
			return "", fmt.Errorf("destination shortcut %q must be destination:<uuid>:<icon>", item)
		}
		if !canonicalUUID.MatchString(uuid) {
			return "", fmt.Errorf("destination shortcut %q has a malformed uuid", item)
		}
		if !shortcutDestinationIcons[icon] {
			return "", fmt.Errorf("unknown destination icon %q", icon)
		}
		return "destination:" + strings.ToLower(uuid), nil
	}
	if !shortcutActionIDs[item] {
		return "", fmt.Errorf("unknown shortcut item %q", item)
	}
	return item, nil
}
