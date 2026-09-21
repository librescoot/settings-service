package destination

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/librescoot/redis-ipc"
	"github.com/redis/go-redis/v9"
)

func startServer(t *testing.T) (*miniredis.Miniredis, *redis_ipc.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	host, portStr, err := net.SplitHostPort(mr.Addr())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("Atoi: %v", err)
	}
	ipc, err := redis_ipc.New(redis_ipc.WithAddress(host), redis_ipc.WithPort(port))
	if err != nil {
		t.Fatalf("redis_ipc.New: %v", err)
	}
	server := NewServer(ipc)
	server.Start()
	t.Cleanup(server.Stop)
	t.Cleanup(func() { ipc.Close() })
	return mr, ipc
}

func callSave(t *testing.T, ipc *redis_ipc.Client, req SaveRequest) SaveResponse {
	t.Helper()
	resp, err := redis_ipc.CallMethod[SaveRequest, SaveResponse](ipc, Channel,
		"destination.save", req, 5*time.Second)
	if err != nil {
		t.Fatalf("save(%+v): %v", req, err)
	}
	return resp
}

func TestSaveAllocatesSlotsAndAssignsUUIDs(t *testing.T) {
	_, ipc := startServer(t)

	first := callSave(t, ipc, SaveRequest{Latitude: 52.5, Longitude: 13.4, Label: "Home"})
	if first.ID != 0 {
		t.Errorf("first id = %d, want 0", first.ID)
	}
	if !ValidUUID(first.UUID) {
		t.Fatalf("first uuid = %q, want canonical uuid", first.UUID)
	}

	second := callSave(t, ipc, SaveRequest{Latitude: 52.6, Longitude: 13.5, Label: "Work"})
	if second.ID != 1 {
		t.Errorf("second id = %d, want 1", second.ID)
	}
	if second.UUID == first.UUID {
		t.Error("two records share a uuid")
	}

	settings, err := ipc.HGetAll(settingsHash)
	if err != nil {
		t.Fatalf("HGetAll: %v", err)
	}
	if got := settings["dashboard.saved-locations.0.latitude"]; got != "52.5000000" {
		t.Errorf("latitude = %q, want 52.5000000", got)
	}
	if settings["dashboard.saved-locations.0.uuid"] != first.UUID {
		t.Errorf("stored uuid = %q, want %q", settings["dashboard.saved-locations.0.uuid"], first.UUID)
	}

	// Updating an existing slot keeps its identity.
	id := 0
	updated := callSave(t, ipc, SaveRequest{ID: &id, Latitude: 52.51, Longitude: 13.41, Label: "Home old"})
	if updated.UUID != first.UUID {
		t.Errorf("updated uuid = %q, want %q", updated.UUID, first.UUID)
	}
	settings, _ = ipc.HGetAll(settingsHash)
	if settings["dashboard.saved-locations.0.label"] != "Home old" {
		t.Errorf("label = %q, want %q", settings["dashboard.saved-locations.0.label"], "Home old")
	}
}

func TestSaveRejectsMalformedRequests(t *testing.T) {
	_, ipc := startServer(t)

	id := MaxLocations
	if _, err := redis_ipc.CallMethod[SaveRequest, SaveResponse](ipc, Channel,
		"destination.save", SaveRequest{ID: &id}, 5*time.Second); err == nil {
		t.Error("out-of-range id accepted")
	}
	if _, err := redis_ipc.CallMethod[SaveRequest, SaveResponse](ipc, Channel,
		"destination.save", SaveRequest{Latitude: 52.5, Longitude: 13.4}, 5*time.Second); err != nil {
		t.Errorf("valid save failed: %v", err)
	}
}

func TestDeleteClearsWholeRecordAndPrunesItem(t *testing.T) {
	mr, ipc := startServer(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	sub := rdb.Subscribe(context.Background(), settingsChannel)
	t.Cleanup(func() { sub.Close() })
	if _, err := sub.Receive(context.Background()); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	resp := callSave(t, ipc, SaveRequest{Latitude: 52.5, Longitude: 13.4, Label: "Home"})

	// Leftovers a record can carry: the item token plus legacy quick fields.
	ipc.HSet(settingsHash, "dashboard.saved-locations.0.quick-slot", "1")
	ipc.HSet(settingsHash, "dashboard.saved-locations.0.quick-icon", "home")
	items := `["view","destination:` + resp.UUID + `:home","theme"]`
	ipc.HSet(settingsHash, shortcutItemsKey, items)

	if _, err := redis_ipc.CallMethod[IDRequest, EmptyResponse](ipc, Channel,
		"destination.delete", IDRequest{ID: resp.ID}, 5*time.Second); err != nil {
		t.Fatalf("delete: %v", err)
	}

	settings, err := ipc.HGetAll(settingsHash)
	if err != nil {
		t.Fatalf("HGetAll: %v", err)
	}
	for key := range settings {
		if len(key) >= len("dashboard.saved-locations.0.") &&
			key[:len("dashboard.saved-locations.0.")] == "dashboard.saved-locations.0." {
			t.Errorf("record field %q survived delete", key)
		}
	}
	gotItems, err := ipc.HGet(settingsHash, shortcutItemsKey)
	if err != nil {
		t.Fatalf("HGet items: %v", err)
	}
	var kept []string
	if err := json.Unmarshal([]byte(gotItems), &kept); err != nil {
		t.Fatalf("items not JSON: %q", gotItems)
	}
	if len(kept) != 2 || kept[0] != "view" || kept[1] != "theme" {
		t.Errorf("items after delete = %v, want [view theme]", kept)
	}

	msg, err := sub.ReceiveMessage(context.Background())
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if msg.Payload != "dashboard.saved-locations.0" {
		t.Errorf("published %q, want record prefix", msg.Payload)
	}
}

func TestTouchRequiresExistingRecord(t *testing.T) {
	mr, ipc := startServer(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	resp := callSave(t, ipc, SaveRequest{Latitude: 52.5, Longitude: 13.4, Label: "Home"})
	// Pin the field so the update is observable without sleeping across the
	// whole second the timestamp resolution covers.
	ipc.HSet(settingsHash, "dashboard.saved-locations.0.last-used-at", "2020-01-01T00:00:00Z")

	if _, err := redis_ipc.CallMethod[IDRequest, EmptyResponse](ipc, Channel,
		"destination.touch", IDRequest{ID: resp.ID}, 5*time.Second); err != nil {
		t.Fatalf("touch: %v", err)
	}
	got, err := ipc.HGet(settingsHash, "dashboard.saved-locations.0.last-used-at")
	if err != nil {
		t.Fatalf("HGet: %v", err)
	}
	if got == "2020-01-01T00:00:00Z" || got == "" {
		t.Errorf("last-used-at = %q, want a fresh timestamp", got)
	}

	if _, err := redis_ipc.CallMethod[IDRequest, EmptyResponse](ipc, Channel,
		"destination.touch", IDRequest{ID: 7}, 5*time.Second); err == nil {
		t.Error("touch on a missing record succeeded")
	}
}

// TestRawEnvelopeWireFormat pins the request and reply shapes an external
// client (the dashboard's C++ caller) builds by hand.
func TestRawEnvelopeWireFormat(t *testing.T) {
	mr, _ := startServer(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	ctx := context.Background()

	replyChannel := Channel + ":reply:wire-test"
	sub := rdb.Subscribe(ctx, replyChannel)
	t.Cleanup(func() { sub.Close() })
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	envelope, err := json.Marshal(map[string]any{
		"id":            "wire-1",
		"method":        "destination.save",
		"reply_channel": replyChannel,
		"deadline":      time.Now().Add(5 * time.Second).UnixMilli(),
		"payload":       map[string]any{"latitude": 1.5, "longitude": 2.5, "label": "Wire"},
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if err := rdb.LPush(ctx, Channel, envelope).Err(); err != nil {
		t.Fatalf("LPUSH: %v", err)
	}
	msg, err := sub.ReceiveMessage(ctx)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	var reply struct {
		OK      bool            `json:"ok"`
		Payload json.RawMessage `json:"payload"`
		Error   string          `json:"error"`
	}
	if err := json.Unmarshal([]byte(msg.Payload), &reply); err != nil {
		t.Fatalf("reply not JSON: %q", msg.Payload)
	}
	if !reply.OK {
		t.Fatalf("reply not ok: %s", reply.Error)
	}
	var saved SaveResponse
	if err := json.Unmarshal(reply.Payload, &saved); err != nil {
		t.Fatalf("reply payload: %v", err)
	}
	if saved.ID != 0 || !ValidUUID(saved.UUID) {
		t.Errorf("reply payload = %+v, want id 0 and a uuid", saved)
	}

	// Unknown methods answer with an error rather than dropping the request.
	envelope, _ = json.Marshal(map[string]any{
		"id":            "wire-2",
		"method":        "destination.nope",
		"reply_channel": replyChannel,
		"deadline":      time.Now().Add(5 * time.Second).UnixMilli(),
		"payload":       map[string]any{},
	})
	if err := rdb.LPush(ctx, Channel, envelope).Err(); err != nil {
		t.Fatalf("LPUSH: %v", err)
	}
	msg, err = sub.ReceiveMessage(ctx)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if err := json.Unmarshal([]byte(msg.Payload), &reply); err != nil {
		t.Fatalf("reply not JSON: %q", msg.Payload)
	}
	if reply.OK || reply.Error == "" {
		t.Errorf("unknown-method reply = %+v, want ok=false with an error", reply)
	}
}

func TestHealUUIDs(t *testing.T) {
	valid := "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	userSet := map[string]struct{}{}

	fields := map[string]any{
		// Missing uuid: healed and marked for persistence.
		"dashboard.saved-locations.0.latitude":  "52.5000000",
		"dashboard.saved-locations.0.longitude": "13.4000000",
		// Existing valid uuid: untouched, not marked.
		"dashboard.saved-locations.1.latitude": "51.5000000",
		"dashboard.saved-locations.1.uuid":     valid,
		// No coordinates: not a record, left alone.
		"dashboard.saved-locations.2.uuid": "0d6c21f0-0000-4000-8000-000000000001",
	}
	if !HealUUIDs(fields, userSet) {
		t.Fatal("HealUUIDs reported nothing healed")
	}
	if !ValidUUID(fields["dashboard.saved-locations.0.uuid"].(string)) {
		t.Errorf("healed uuid = %v", fields["dashboard.saved-locations.0.uuid"])
	}
	if _, ok := userSet["dashboard.saved-locations.0"]; !ok {
		t.Error("healed record not marked user-set")
	}
	if _, ok := userSet["dashboard.saved-locations.1"]; ok {
		t.Error("record with a valid uuid was marked user-set")
	}
	if fields["dashboard.saved-locations.2.uuid"] != "0d6c21f0-0000-4000-8000-000000000001" {
		t.Error("coordinate-less record was modified")
	}
	if HealUUIDs(fields, userSet) {
		t.Error("second pass healed again; healing is not idempotent")
	}
}
