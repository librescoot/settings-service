package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/librescoot/redis-ipc"
	"github.com/librescoot/settings-service/internal/config"
	"github.com/librescoot/settings-service/internal/destination"
	"github.com/librescoot/settings-service/internal/routeplan"
)

func TestRoutePlanBootUsesSettingsFileDirectory(t *testing.T) {
	mr := miniredis.RunT(t)
	old := config.TomlFilePath
	config.TomlFilePath = filepath.Join(t.TempDir(), "settings.toml")
	t.Cleanup(func() { config.TomlFilePath = old })
	svc, err := New(mr.Addr(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.LoadSettingsFromTOML(); err != nil {
		t.Fatal(err)
	}
	host, port, err := destination.ParseAddr(mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	ipc, err := redis_ipc.New(redis_ipc.WithAddress(host), redis_ipc.WithPort(port))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := redis_ipc.CallMethod[routeplan.AppendRequest, routeplan.Plan](ipc, routeplan.Channel, "plan.append", routeplan.AppendRequest{Stop: routeplan.StopInput{Lat: 52, Lon: 13}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ipc.Close()
	svc.Close()
	reloaded, err := New(mr.Addr(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	if err := reloaded.LoadSettingsFromTOML(); err != nil {
		t.Fatal(err)
	}
	if got := mr.HGet("navigation", "revision"); got != "1" || len(plan.Stops) != 1 {
		t.Fatalf("restart revision %q, initial plan %+v", got, plan)
	}
}
