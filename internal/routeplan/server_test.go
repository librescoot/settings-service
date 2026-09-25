package routeplan

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/librescoot/redis-ipc"
)

func client(t *testing.T, addr string) *redis_ipc.Client {
	t.Helper()
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	ipc, err := redis_ipc.New(redis_ipc.WithAddress(host), redis_ipc.WithPort(port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ipc.Close() })
	return ipc
}
func rpc[Q, R any](t *testing.T, ipc *redis_ipc.Client, method string, req Q) R {
	t.Helper()
	response, err := redis_ipc.CallMethod[Q, R](ipc, Channel, method, req, 2*time.Second)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return response
}
func TestSerializedAppendsAndStaleProgress(t *testing.T) {
	mr := miniredis.RunT(t)
	ipc := client(t, mr.Addr())
	s := NewServer(ipc, filepath.Join(t.TempDir(), "route.json"))
	if err := s.Load(nil); err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Stop()
	a := rpc[AppendRequest, Plan](t, ipc, "plan.append", AppendRequest{Stop: StopInput{52, 13, "A"}})
	b := rpc[AppendRequest, Plan](t, ipc, "plan.append", AppendRequest{Stop: StopInput{53, 14, "B"}})
	if len(b.Stops) != 2 || b.Revision != a.Revision+1 {
		t.Fatalf("rapid append lost a stop: %+v", b)
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			other := client(t, mr.Addr())
			_, err := redis_ipc.CallMethod[AppendRequest, Plan](other, Channel, "plan.append", AppendRequest{Stop: StopInput{float64(54 + i), 15, ""}}, 2*time.Second)
			if err != nil {
				t.Errorf("concurrent append: %v", err)
			}
		}(i)
	}
	wg.Wait()
	current := rpc[Empty, Plan](t, ipc, "plan.get", Empty{})
	if len(current.Stops) != 4 {
		t.Fatalf("concurrent append lost stop: %+v", current)
	}
	replaced := rpc[ReplaceRequest, Plan](t, ipc, "plan.replace", ReplaceRequest{Stops: []StopInput{{51, 12, "New"}}})
	if replaced.ID == current.ID {
		t.Fatal("replacement reused plan id")
	}
	for _, method := range []string{"plan.reached", "plan.advance"} {
		if _, err := redis_ipc.CallMethod[ProgressRequest, Plan](ipc, Channel, method, ProgressRequest{current.ID, current.Stops[0].ID}, time.Second); err == nil {
			t.Errorf("%s accepted stale event", method)
		}
	}
	if _, err := redis_ipc.CallMethod[RemoveRequest, Plan](ipc, Channel, "plan.remove", RemoveRequest{Index: 0, ExpectedRevision: current.Revision}, time.Second); err == nil {
		t.Fatal("stale remove succeeded")
	}
	if _, err := redis_ipc.CallMethod[ClearRequest, Plan](ipc, Channel, "plan.clear", ClearRequest{ExpectedPlanID: current.ID}, time.Second); err == nil {
		t.Fatal("stale clear succeeded")
	}
	projected := mr.HGet("navigation", "plan")
	var p Plan
	if err := json.Unmarshal([]byte(projected), &p); err != nil || p.ID != replaced.ID {
		t.Fatalf("projection %q: %v", projected, err)
	}
}
func TestMoveJumpAndUnreachGuardedByOwner(t *testing.T) {
	mr := miniredis.RunT(t)
	ipc := client(t, mr.Addr())
	s := NewServer(ipc, filepath.Join(t.TempDir(), "plan.json"))
	if err := s.Load(nil); err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Stop()
	original := rpc[ReplaceRequest, Plan](t, ipc, "plan.replace", ReplaceRequest{Stops: []StopInput{{52, 13, "First"}, {53, 14, "Second"}, {54, 15, "Third"}}})
	invalidStep := 3
	if _, err := redis_ipc.CallMethod[ReplaceRequest, Plan](ipc, Channel, "plan.replace", ReplaceRequest{Stops: []StopInput{{52, 13, "First"}}, StartStep: &invalidStep}, time.Second); err == nil {
		t.Fatal("out-of-range start_step accepted")
	}
	if current := rpc[Empty, Plan](t, ipc, "plan.get", Empty{}); current.ID != original.ID {
		t.Fatal("invalid replacement changed the plan")
	}
	reached := rpc[ProgressRequest, Plan](t, ipc, "plan.reached", ProgressRequest{original.ID, original.Stops[0].ID})
	moved := rpc[MoveRequest, Plan](t, ipc, "plan.move", MoveRequest{FromIndex: 0, ToIndex: 2, ExpectedRevision: reached.Revision})
	if moved.CurrentStep != 2 || moved.Stops[2].ID != original.Stops[0].ID || !moved.Stops[2].Reached || mr.HGet("navigation", "destination") != "52.000000,13.000000" {
		t.Fatalf("move changed current stop: %+v", moved)
	}
	if _, err := redis_ipc.CallMethod[JumpRequest, Plan](ipc, Channel, "plan.jump", JumpRequest{Index: 1, ExpectedRevision: reached.Revision}, time.Second); err == nil {
		t.Fatal("stale jump succeeded")
	}
	jumped := rpc[JumpRequest, Plan](t, ipc, "plan.jump", JumpRequest{Index: 1, ExpectedRevision: moved.Revision})
	if jumped.Stops[0].ID != moved.Stops[0].ID || !jumped.Stops[0].Reached || jumped.Stops[1].Reached || jumped.Stops[2].Reached {
		t.Fatalf("jump changed IDs or reached flags: %+v", jumped)
	}
	current := ProgressRequest{jumped.ID, jumped.Stops[1].ID}
	marked := rpc[ProgressRequest, Plan](t, ipc, "plan.reached", current)
	undone := rpc[ProgressRequest, Plan](t, ipc, "plan.unreach", current)
	if undone.Stops[1].Reached || undone.Revision != marked.Revision+1 {
		t.Fatalf("unreach did not undo arrival: %+v", undone)
	}
	if _, err := redis_ipc.CallMethod[ProgressRequest, Plan](ipc, Channel, "plan.unreach", ProgressRequest{jumped.ID, jumped.Stops[0].ID}, time.Second); err == nil {
		t.Fatal("stale unreach succeeded")
	}
}

func TestReplaceSelectsStartStepAtomically(t *testing.T) {
	mr := miniredis.RunT(t)
	ipc := client(t, mr.Addr())
	s := NewServer(ipc, filepath.Join(t.TempDir(), "plan.json"))
	if err := s.Load(nil); err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Stop()
	start := 1
	plan := rpc[ReplaceRequest, Plan](t, ipc, "plan.replace", ReplaceRequest{Stops: []StopInput{{52, 13, "First"}, {53, 14, "Second"}}, StartStep: &start})
	if plan.CurrentStep != 1 || !plan.Stops[0].Reached || plan.Stops[1].Reached || mr.HGet("navigation", "destination") != "53.000000,14.000000" {
		t.Fatalf("replacement not atomic: %+v", plan)
	}
}

func TestReachedAdvanceAndDurableClear(t *testing.T) {
	mr := miniredis.RunT(t)
	ipc := client(t, mr.Addr())
	path := filepath.Join(t.TempDir(), "plan.json")
	s := NewServer(ipc, path)
	if err := s.Load(nil); err != nil {
		t.Fatal(err)
	}
	s.Start()
	first := rpc[AppendRequest, Plan](t, ipc, "plan.append", AppendRequest{Stop: StopInput{50, 10, "First"}})
	if len(first.Stops) != 1 {
		t.Fatalf("first stop: %+v", first)
	}
	plan := rpc[AppendRequest, Plan](t, ipc, "plan.append", AppendRequest{Stop: StopInput{52, 13, "Next"}})
	advanced := rpc[ProgressRequest, Plan](t, ipc, "plan.advance", ProgressRequest{plan.ID, plan.Stops[0].ID})
	if advanced.CurrentStep != 1 || advanced.Stops[0].Reached || mr.HGet("navigation", "destination") != "52.000000,13.000000" {
		t.Fatalf("skip projection: %+v", advanced)
	}
	reached := rpc[ProgressRequest, Plan](t, ipc, "plan.reached", ProgressRequest{plan.ID, plan.Stops[1].ID})
	if !reached.Stops[1].Reached {
		t.Fatal("reach not recorded")
	}
	s.Stop()
	mr.FlushAll() // Redis state is volatile; the disk snapshot is authoritative.
	restart := NewServer(ipc, path)
	if err := restart.Load(nil); err != nil {
		t.Fatal(err)
	}
	restart.Start()
	restored := rpc[Empty, Plan](t, ipc, "plan.get", Empty{})
	if restored.CurrentStep != 1 || restored.Revision != reached.Revision {
		t.Fatalf("restore: %+v", restored)
	}
	cleared := rpc[ClearRequest, Plan](t, ipc, "plan.clear", ClearRequest{ExpectedPlanID: restored.ID})
	if len(cleared.Stops) != 0 || cleared.Revision <= restored.Revision {
		t.Fatalf("clear: %+v", cleared)
	}
	restart.Stop()
	// A stale legacy source must never reappear after a committed empty snapshot.
	mr.HSet("navigation", "waypoints", `[{"lat":50,"lon":10}]`)
	again := NewServer(ipc, path)
	if err := again.Load(map[string]any{"dashboard.route-plan.active": "true", "dashboard.route-plan.0.latitude": "52", "dashboard.route-plan.0.longitude": "13"}); err != nil {
		t.Fatal(err)
	}
	again.Start()
	defer again.Stop()
	empty := rpc[Empty, Plan](t, ipc, "plan.get", Empty{})
	if len(empty.Stops) != 0 || empty.Revision != cleared.Revision || mr.HGet("navigation", "destination") != "" {
		t.Fatalf("clear resurrected: %+v", empty)
	}
}
func TestActiveLegacySettingsMigration(t *testing.T) {
	mr := miniredis.RunT(t)
	ipc := client(t, mr.Addr())
	path := filepath.Join(t.TempDir(), "plan.json")
	s := NewServer(ipc, path)
	fields := map[string]any{
		"dashboard.route-plan.active":       true,
		"dashboard.route-plan.0.latitude":   52.5,
		"dashboard.route-plan.0.longitude":  13.4,
		"dashboard.route-plan.0.label":      "Home",
		"dashboard.route-plan.0.reached":    true,
		"dashboard.route-plan.1.latitude":   52.6,
		"dashboard.route-plan.1.longitude":  13.5,
		"dashboard.route-plan.current-step": 1,
	}
	if err := s.Load(fields); err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Stop()
	p := rpc[Empty, Plan](t, ipc, "plan.get", Empty{})
	if len(p.Stops) != 2 || !p.Stops[0].Reached || p.CurrentStep != 1 || p.ID == "" {
		t.Fatalf("legacy migration: %+v", p)
	}
	if mr.HGet("navigation", "address") != "" || mr.HGet("navigation", "destination") != "52.600000,13.500000" {
		t.Fatal("incoherent target projection")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var disk Plan
	if err := json.Unmarshal(data, &disk); err != nil || disk.ID != p.ID {
		t.Fatalf("disk snapshot: %+v %v", disk, err)
	}
}

func TestLegacyNavigationWithoutActiveSettingsIsNotImported(t *testing.T) {
	mr := miniredis.RunT(t)
	ipc := client(t, mr.Addr())
	mr.HSet("navigation", "waypoints", `[{"lat":52,"lon":13,"label":"Possibly stale"}]`)
	s := NewServer(ipc, filepath.Join(t.TempDir(), "plan.json"))
	if err := s.Load(nil); err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Stop()
	plan := rpc[Empty, Plan](t, ipc, "plan.get", Empty{})
	if len(plan.Stops) != 0 || mr.HGet("navigation", "waypoints") != "" {
		t.Fatalf("unverified legacy target resurrected: %+v", plan)
	}
}

func TestInactiveLegacySettingsSuppressRedisTarget(t *testing.T) {
	mr := miniredis.RunT(t)
	ipc := client(t, mr.Addr())
	mr.HSet("navigation", "destination", "52.000000,13.000000")
	mr.HSet("navigation", "latitude", "52")
	mr.HSet("navigation", "longitude", "13")
	s := NewServer(ipc, filepath.Join(t.TempDir(), "plan.json"))
	if err := s.Load(map[string]any{"dashboard.route-plan.active": false}); err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Stop()
	plan := rpc[Empty, Plan](t, ipc, "plan.get", Empty{})
	if len(plan.Stops) != 0 || mr.HGet("navigation", "destination") != "" {
		t.Fatalf("inactive legacy plan resurrected: %+v", plan)
	}
}

func TestStopWireRequiresCoordinates(t *testing.T) {
	var stop StopInput
	if err := json.Unmarshal([]byte(`{"lat":52}`), &stop); err == nil {
		t.Fatal("missing longitude accepted")
	}
	var appendRequest AppendRequest
	if err := json.Unmarshal([]byte(`{}`), &appendRequest); err == nil {
		t.Fatal("missing stop accepted")
	}
}

func TestPersistenceFailureDoesNotMutatePlan(t *testing.T) {
	mr := miniredis.RunT(t)
	ipc := client(t, mr.Addr())
	path := filepath.Join(t.TempDir(), "plan.json")
	s := NewServer(ipc, path)
	if err := s.Load(nil); err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Stop()
	if err := os.Mkdir(path+".tmp", 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := redis_ipc.CallMethod[AppendRequest, Plan](ipc, Channel, "plan.append", AppendRequest{Stop: StopInput{52, 13, ""}}, time.Second); err == nil {
		t.Fatal("append succeeded despite failed persistence")
	}
	p := rpc[Empty, Plan](t, ipc, "plan.get", Empty{})
	if p.Revision != 0 || len(p.Stops) != 0 {
		t.Fatalf("failed write mutated plan: %+v", p)
	}
}
