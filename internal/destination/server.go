// Package destination exposes the saved-location record lifecycle over the
// redis-ipc RPC channel. It is the single writer for these records: slot
// allocation, UUID assignment, whole-record deletion, and pruning of the
// record's destination token from the shortcut-menu items list all happen
// here, so no caller has to know the record's field set.
package destination

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/librescoot/redis-ipc"
	"github.com/redis/go-redis/v9"
)

const (
	// Channel is the request queue for destination management calls.
	Channel = "settings:destinations"

	settingsHash     = "settings"
	settingsChannel  = "settings"
	recordKeyPrefix  = "dashboard.saved-locations"
	shortcutItemsKey = "dashboard.shortcut-menu.items"

	// MaxLocations matches the saved-location record templates in the schema.
	MaxLocations = 30
)

var recordLatitudeKey = regexp.MustCompile(`^dashboard\.saved-locations\.(\d+)\.latitude$`)
var canonicalUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// SaveRequest upserts one record. A nil ID allocates the first free slot.
type SaveRequest struct {
	ID        *int    `json:"id,omitempty"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Label     string  `json:"label"`
}

type SaveResponse struct {
	ID   int    `json:"id"`
	UUID string `json:"uuid"`
}

// IDRequest addresses one record by slot.
type IDRequest struct {
	ID int `json:"id"`
}

type EmptyResponse struct{}

type Server struct {
	ipc  *redis_ipc.Client
	call *redis_ipc.CallServer
}

// NewServer registers the destination methods on a CallServer bound to the
// destination channel. Concurrency is capped at one handler: concurrent saves
// must not pick the same free slot.
func NewServer(ipc *redis_ipc.Client) *Server {
	s := &Server{ipc: ipc}
	cs := redis_ipc.NewCallServer(ipc, Channel, redis_ipc.WithCallServerConcurrency(1))
	redis_ipc.RegisterCall[SaveRequest, SaveResponse](cs, "destination.save", s.save)
	redis_ipc.RegisterCall[IDRequest, EmptyResponse](cs, "destination.delete", s.delete)
	redis_ipc.RegisterCall[IDRequest, EmptyResponse](cs, "destination.touch", s.touch)
	s.call = cs
	return s
}

func (s *Server) Start() { s.call.Start() }

func (s *Server) Stop() { s.call.Stop() }

func (s *Server) save(req SaveRequest) (SaveResponse, error) {
	slot := -1
	if req.ID != nil {
		slot = *req.ID
		if slot < 0 || slot >= MaxLocations {
			return SaveResponse{}, fmt.Errorf("location id %d out of range", slot)
		}
	}

	settings, err := s.ipc.HGetAll(settingsHash)
	if err != nil {
		return SaveResponse{}, fmt.Errorf("read settings: %w", err)
	}
	if slot < 0 {
		slot = firstFreeSlot(settings)
		if slot < 0 {
			return SaveResponse{}, errors.New("all saved-location slots are full")
		}
	}

	prefix := recordPrefix(slot)
	uuid := settings[prefix+".uuid"]
	if !ValidUUID(uuid) {
		if uuid, err = NewUUID(); err != nil {
			return SaveResponse{}, err
		}
	}
	now := timestamp()
	createdAt := settings[prefix+".created-at"]
	if createdAt == "" {
		createdAt = now
	}
	for field, value := range map[string]string{
		prefix + ".latitude":     strconv.FormatFloat(req.Latitude, 'f', 7, 64),
		prefix + ".longitude":    strconv.FormatFloat(req.Longitude, 'f', 7, 64),
		prefix + ".label":        req.Label,
		prefix + ".uuid":         uuid,
		prefix + ".created-at":   createdAt,
		prefix + ".last-used-at": now,
	} {
		if err := s.ipc.HSet(settingsHash, field, value); err != nil {
			return SaveResponse{}, fmt.Errorf("write %s: %w", field, err)
		}
	}
	// The record-prefix notification drives settings-service persistence.
	if _, err := s.ipc.Publish(settingsChannel, prefix); err != nil {
		return SaveResponse{}, fmt.Errorf("publish record: %w", err)
	}
	return SaveResponse{ID: slot, UUID: uuid}, nil
}

func (s *Server) delete(req IDRequest) (EmptyResponse, error) {
	if req.ID < 0 || req.ID >= MaxLocations {
		return EmptyResponse{}, fmt.Errorf("location id %d out of range", req.ID)
	}
	settings, err := s.ipc.HGetAll(settingsHash)
	if err != nil {
		return EmptyResponse{}, fmt.Errorf("read settings: %w", err)
	}
	prefix := recordPrefix(req.ID)

	// Prune before mutating: a failing prune leaves the record retryable.
	if uuid := settings[prefix+".uuid"]; uuid != "" {
		if err := s.pruneShortcutItem(uuid); err != nil {
			return EmptyResponse{}, err
		}
	}

	var fields []string
	for key := range settings {
		if strings.HasPrefix(key, prefix+".") {
			fields = append(fields, key)
		}
	}
	if len(fields) > 0 {
		if err := s.ipc.Raw().HDel(s.ipc.Context(), settingsHash, fields...).Err(); err != nil {
			return EmptyResponse{}, fmt.Errorf("clear record: %w", err)
		}
	}
	if _, err := s.ipc.Publish(settingsChannel, prefix); err != nil {
		return EmptyResponse{}, fmt.Errorf("publish record: %w", err)
	}
	return EmptyResponse{}, nil
}

func (s *Server) touch(req IDRequest) (EmptyResponse, error) {
	if req.ID < 0 || req.ID >= MaxLocations {
		return EmptyResponse{}, fmt.Errorf("location id %d out of range", req.ID)
	}
	settings, err := s.ipc.HGetAll(settingsHash)
	if err != nil {
		return EmptyResponse{}, fmt.Errorf("read settings: %w", err)
	}
	prefix := recordPrefix(req.ID)
	if settings[prefix+".latitude"] == "" {
		return EmptyResponse{}, fmt.Errorf("location %d not found", req.ID)
	}
	field := prefix + ".last-used-at"
	if err := s.ipc.HSet(settingsHash, field, timestamp()); err != nil {
		return EmptyResponse{}, fmt.Errorf("write %s: %w", field, err)
	}
	if _, err := s.ipc.Publish(settingsChannel, field); err != nil {
		return EmptyResponse{}, fmt.Errorf("publish %s: %w", field, err)
	}
	return EmptyResponse{}, nil
}

// pruneShortcutItem drops every destination token for uuid from the
// shortcut-menu items list so a deleted record cannot stay selectable.
func (s *Server) pruneShortcutItem(uuid string) error {
	raw, err := s.ipc.HGet(settingsHash, shortcutItemsKey)
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read shortcut items: %w", err)
	}
	if raw == "" {
		return nil
	}
	var items []string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil // an invalid list is repaired by schema validation, not here
	}
	kept := make([]string, 0, len(items))
	for _, item := range items {
		if parts := strings.Split(item, ":"); len(parts) == 3 &&
			parts[0] == "destination" && strings.EqualFold(parts[1], uuid) {
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) == len(items) {
		return nil
	}
	encoded, err := json.Marshal(kept)
	if err != nil {
		return fmt.Errorf("encode shortcut items: %w", err)
	}
	if err := s.ipc.HSet(settingsHash, shortcutItemsKey, string(encoded)); err != nil {
		return fmt.Errorf("write shortcut items: %w", err)
	}
	if _, err := s.ipc.Publish(settingsChannel, shortcutItemsKey); err != nil {
		return fmt.Errorf("publish shortcut items: %w", err)
	}
	return nil
}

// HealUUIDs assigns a UUID to every saved-location record hydrated without
// one and marks the record user-set so the assignment persists. It reports
// whether anything was healed. Records without coordinates are left alone;
// their leftover fields are not a record.
func HealUUIDs(fields map[string]any, userSet map[string]struct{}) bool {
	healed := false
	for key := range fields {
		match := recordLatitudeKey.FindStringSubmatch(key)
		if match == nil {
			continue
		}
		record := recordKeyPrefix + "." + match[1]
		uuidKey := record + ".uuid"
		if existing, ok := fields[uuidKey].(string); ok && ValidUUID(existing) {
			continue
		}
		uuid, err := NewUUID()
		if err != nil {
			log.Printf("Skipping UUID heal for %s: %v", record, err)
			continue
		}
		fields[uuidKey] = uuid
		userSet[record] = struct{}{}
		healed = true
	}
	return healed
}

// ParseAddr splits a host:port address for redis-ipc, defaulting a missing
// port to 6379.
func ParseAddr(addr string) (string, int, error) {
	const defaultPort = 6379
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.Contains(err.Error(), "missing port in address") {
			return addr, defaultPort, nil
		}
		return "", 0, fmt.Errorf("invalid redis address %q: %w", addr, err)
	}
	port, convErr := strconv.Atoi(portStr)
	if convErr != nil {
		return "", 0, fmt.Errorf("invalid port %q", portStr)
	}
	return host, port, nil
}

func firstFreeSlot(settings map[string]string) int {
	for i := 0; i < MaxLocations; i++ {
		if settings[recordPrefix(i)+".latitude"] == "" {
			return i
		}
	}
	return -1
}

func recordPrefix(id int) string {
	return recordKeyPrefix + "." + strconv.Itoa(id)
}

// timestamp matches the Qt ISODate rendering of a UTC timestamp.
func timestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

// NewUUID returns a random RFC 4122 version 4 UUID in canonical form.
func NewUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate uuid: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	dst := make([]byte, 36)
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst), nil
}

// ValidUUID reports whether value is a canonical 8-4-4-4-12 UUID.
func ValidUUID(value string) bool {
	return canonicalUUID.MatchString(value)
}
