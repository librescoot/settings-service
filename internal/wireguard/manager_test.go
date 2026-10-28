package wireguard

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestActivateProfilesRetriesUntilSuccessful(t *testing.T) {
	m := newManager(t.TempDir())
	m.initialRetry = time.Millisecond
	m.maximumRetry = time.Millisecond

	calls := 0
	m.activate = func(context.Context, string) error {
		calls++
		if calls < 3 {
			return errors.New("uplink not ready")
		}
		return nil
	}

	if err := m.activateProfiles(context.Background(), []string{"management"}); err != nil {
		t.Fatalf("activateProfiles returned error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("activation attempts = %d, want 3", calls)
	}
}

func TestActivateProfilesStopsWhenCancelled(t *testing.T) {
	m := newManager(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.activate = func(context.Context, string) error {
		return errors.New("activation failed")
	}

	err := m.activateProfiles(ctx, []string{"management"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("activateProfiles error = %v, want context.Canceled", err)
	}
}

func TestActivateProfilesWithNoProfilesReturns(t *testing.T) {
	m := newManager(t.TempDir())
	m.activate = func(context.Context, string) error {
		t.Fatal("activate called without profiles")
		return nil
	}

	if err := m.activateProfiles(context.Background(), nil); err != nil {
		t.Fatalf("activateProfiles returned error: %v", err)
	}
}
