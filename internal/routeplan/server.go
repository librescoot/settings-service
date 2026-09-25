package routeplan

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/librescoot/redis-ipc"
	"github.com/librescoot/settings-service/internal/destination"
	"github.com/librescoot/settings-service/internal/fileutil"
)

const Channel = "settings:route-plan"

type Stop struct {
	ID      string  `json:"id"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Label   string  `json:"label"`
	Reached bool    `json:"reached"`
}
type Plan struct {
	ID          string `json:"id"`
	Revision    uint64 `json:"revision"`
	Stops       []Stop `json:"stops"`
	CurrentStep int    `json:"current_step"`
}
type Empty struct{}
type ReplaceRequest struct {
	Stops []StopInput `json:"stops"`
}
type StopInput struct {
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
	Label string  `json:"label"`
}

func (s *StopInput) UnmarshalJSON(data []byte) error {
	var raw struct {
		Lat   *float64 `json:"lat"`
		Lon   *float64 `json:"lon"`
		Label string   `json:"label"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Lat == nil || raw.Lon == nil {
		return errors.New("stop requires lat and lon")
	}
	*s = StopInput{Lat: *raw.Lat, Lon: *raw.Lon, Label: raw.Label}
	return validateStop(*s)
}

func (r *AppendRequest) UnmarshalJSON(data []byte) error {
	var raw struct {
		Stop *StopInput `json:"stop"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Stop == nil {
		return errors.New("missing stop")
	}
	r.Stop = *raw.Stop
	return nil
}

type AppendRequest struct {
	Stop StopInput `json:"stop"`
}
type RemoveRequest struct {
	Index            int    `json:"index"`
	ExpectedRevision uint64 `json:"expected_revision"`
}
type ProgressRequest struct {
	ExpectedPlanID string `json:"expected_plan_id"`
	ExpectedStopID string `json:"expected_stop_id"`
}
type ClearRequest struct {
	ExpectedPlanID string `json:"expected_plan_id,omitempty"`
}

type Server struct {
	ipc  *redis_ipc.Client
	call *redis_ipc.CallServer
	path string
	plan Plan
}

func NewServer(ipc *redis_ipc.Client, path string) *Server {
	s := &Server{ipc: ipc, path: path, plan: Plan{Stops: []Stop{}}}
	cs := redis_ipc.NewCallServer(ipc, Channel, redis_ipc.WithCallServerConcurrency(1))
	redis_ipc.RegisterCall[Empty, Plan](cs, "plan.get", s.get)
	redis_ipc.RegisterCall[ReplaceRequest, Plan](cs, "plan.replace", s.replace)
	redis_ipc.RegisterCall[AppendRequest, Plan](cs, "plan.append", s.append)
	redis_ipc.RegisterCall[RemoveRequest, Plan](cs, "plan.remove", s.remove)
	redis_ipc.RegisterCall[ProgressRequest, Plan](cs, "plan.reached", s.reached)
	redis_ipc.RegisterCall[ProgressRequest, Plan](cs, "plan.advance", s.advance)
	redis_ipc.RegisterCall[ClearRequest, Plan](cs, "plan.clear", s.clear)
	s.call = cs
	return s
}
func (s *Server) Start() { s.call.Start() }
func (s *Server) Stop()  { s.call.Stop() }

// Load installs the durable snapshot before accepting requests. A present empty snapshot
// is authoritative over any legacy Redis or TOML state.
func (s *Server) Load(settings map[string]any) error {
	data, err := os.ReadFile(s.path)
	if err == nil {
		if err = json.Unmarshal(data, &s.plan); err != nil {
			return fmt.Errorf("decode route plan: %w", err)
		}
		if s.plan.Stops == nil {
			s.plan.Stops = []Stop{}
		}
		if err = validatePlan(s.plan); err != nil {
			return fmt.Errorf("invalid route plan snapshot: %w", err)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err = s.migrate(settings); err != nil {
			return err
		}
		if err = s.persist(s.plan); err != nil {
			return err
		}
	} else {
		return err
	}
	return s.publish(s.plan)
}
func validateStop(stop StopInput) error {
	if math.IsNaN(stop.Lat) || math.IsInf(stop.Lat, 0) || stop.Lat < -90 || stop.Lat > 90 || math.IsNaN(stop.Lon) || math.IsInf(stop.Lon, 0) || stop.Lon < -180 || stop.Lon > 180 {
		return errors.New("invalid stop coordinates")
	}
	return nil
}
func validatePlan(p Plan) error {
	if len(p.Stops) > 32 || p.CurrentStep < 0 || (len(p.Stops) > 0 && p.CurrentStep >= len(p.Stops)) || (len(p.Stops) == 0 && p.CurrentStep != 0) {
		return errors.New("invalid stop count or current_step")
	}
	if len(p.Stops) > 0 && p.ID == "" {
		return errors.New("missing plan id")
	}
	seen := make(map[string]bool)
	for _, stop := range p.Stops {
		if stop.ID == "" || seen[stop.ID] {
			return errors.New("missing or duplicate stop id")
		}
		seen[stop.ID] = true
		if err := validateStop(StopInput{stop.Lat, stop.Lon, stop.Label}); err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) persist(p Plan) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return fileutil.AtomicWrite(s.path, 0644, func(f *os.File) error { _, err := f.Write(data); return err })
}
func (s *Server) publish(p Plan) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	fields := map[string]interface{}{"plan": string(data), "revision": strconv.FormatUint(p.Revision, 10), "waypoints": "", "current-step": "", "destination": "", "latitude": "", "longitude": "", "address": "", "timestamp": ""}
	if len(p.Stops) > 0 {
		waypoints := make([]StopInput, len(p.Stops))
		for i, stop := range p.Stops {
			waypoints[i] = StopInput{stop.Lat, stop.Lon, stop.Label}
		}
		encoded, _ := json.Marshal(waypoints)
		target := p.Stops[p.CurrentStep]
		lat, lon := fmt.Sprintf("%.6f", target.Lat), fmt.Sprintf("%.6f", target.Lon)
		fields["waypoints"] = string(encoded)
		fields["current-step"] = strconv.Itoa(p.CurrentStep)
		fields["destination"] = lat + "," + lon
		fields["latitude"] = lat
		fields["longitude"] = lon
		fields["address"] = target.Label
		fields["timestamp"] = time.Now().UTC().Format(time.RFC3339)
	}
	// HSET of the complete projection is atomic. Notifications follow the write.
	if err := s.ipc.Raw().HSet(s.ipc.Context(), "navigation", fields).Err(); err != nil {
		return err
	}
	for _, key := range []string{"plan", "revision", "waypoints", "current-step", "destination", "latitude", "longitude", "address", "timestamp", "updated"} {
		if _, err := s.ipc.Publish("navigation", key); err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) commit(p Plan) (Plan, error) {
	p.Revision = s.plan.Revision + 1
	if err := s.persist(p); err != nil {
		return Plan{}, err
	}
	s.plan = p
	if err := s.publish(p); err != nil {
		return Plan{}, err
	}
	return p, nil
}
func (s *Server) get(Empty) (Plan, error) { return s.plan, nil }
func newStop(input StopInput) (Stop, error) {
	if err := validateStop(input); err != nil {
		return Stop{}, err
	}
	id, err := destination.NewUUID()
	return Stop{ID: id, Lat: input.Lat, Lon: input.Lon, Label: input.Label}, err
}
func (s *Server) replace(req ReplaceRequest) (Plan, error) {
	if len(req.Stops) == 0 || len(req.Stops) > 32 {
		return Plan{}, errors.New("stops must contain 1 to 32 entries")
	}
	id, err := destination.NewUUID()
	if err != nil {
		return Plan{}, err
	}
	p := Plan{ID: id, Stops: make([]Stop, 0, len(req.Stops))}
	for _, input := range req.Stops {
		stop, err := newStop(input)
		if err != nil {
			return Plan{}, err
		}
		p.Stops = append(p.Stops, stop)
	}
	return s.commit(p)
}
func (s *Server) append(req AppendRequest) (Plan, error) {
	if len(s.plan.Stops) >= 32 {
		return Plan{}, errors.New("plan has 32 stops")
	}
	stop, err := newStop(req.Stop)
	if err != nil {
		return Plan{}, err
	}
	p := s.plan
	p.Stops = append(append([]Stop{}, p.Stops...), stop)
	if p.ID == "" {
		p.ID, err = destination.NewUUID()
		if err != nil {
			return Plan{}, err
		}
	}
	return s.commit(p)
}
func (s *Server) remove(req RemoveRequest) (Plan, error) {
	if req.ExpectedRevision != s.plan.Revision {
		return Plan{}, errors.New("stale revision")
	}
	if req.Index < 0 || req.Index >= len(s.plan.Stops) {
		return Plan{}, errors.New("invalid stop index")
	}
	p := s.plan
	p.Stops = append(append([]Stop{}, p.Stops[:req.Index]...), p.Stops[req.Index+1:]...)
	if len(p.Stops) == 0 {
		p.ID = ""
		p.CurrentStep = 0
	} else if p.CurrentStep >= len(p.Stops) {
		p.CurrentStep = len(p.Stops) - 1
	} else if req.Index < p.CurrentStep {
		p.CurrentStep--
	}
	return s.commit(p)
}
func (s *Server) checkProgress(req ProgressRequest) error {
	if req.ExpectedPlanID == "" || req.ExpectedPlanID != s.plan.ID || len(s.plan.Stops) == 0 || req.ExpectedStopID == "" || req.ExpectedStopID != s.plan.Stops[s.plan.CurrentStep].ID {
		return errors.New("stale plan or stop")
	}
	return nil
}
func (s *Server) reached(req ProgressRequest) (Plan, error) {
	if err := s.checkProgress(req); err != nil {
		return Plan{}, err
	}
	if s.plan.Stops[s.plan.CurrentStep].Reached {
		return s.plan, nil
	}
	p := s.plan
	p.Stops = append([]Stop{}, p.Stops...)
	p.Stops[p.CurrentStep].Reached = true
	return s.commit(p)
}
func (s *Server) advance(req ProgressRequest) (Plan, error) {
	if err := s.checkProgress(req); err != nil {
		return Plan{}, err
	}
	if !s.plan.Stops[s.plan.CurrentStep].Reached || s.plan.CurrentStep+1 >= len(s.plan.Stops) {
		return Plan{}, errors.New("current stop not reached or no next stop")
	}
	p := s.plan
	p.CurrentStep++
	return s.commit(p)
}
func (s *Server) clear(req ClearRequest) (Plan, error) {
	if req.ExpectedPlanID != "" && req.ExpectedPlanID != s.plan.ID {
		return Plan{}, errors.New("stale plan")
	}
	return s.commit(Plan{Stops: []Stop{}})
}
func (s *Server) migrate(settings map[string]any) error {
	// Legacy settings take precedence when marked active; otherwise use the navigation projection.
	if fmt.Sprint(settings["dashboard.route-plan.active"]) == "true" {
		var indices []int
		for key := range settings {
			if strings.HasPrefix(key, "dashboard.route-plan.") && strings.HasSuffix(key, ".latitude") {
				middle := strings.TrimSuffix(strings.TrimPrefix(key, "dashboard.route-plan."), ".latitude")
				if n, err := strconv.Atoi(middle); err == nil && n >= 0 && n < 32 {
					indices = append(indices, n)
				}
			}
		}
		sort.Ints(indices)
		for _, i := range indices {
			prefix := fmt.Sprintf("dashboard.route-plan.%d.", i)
			lat, e1 := strconv.ParseFloat(fmt.Sprint(settings[prefix+"latitude"]), 64)
			lon, e2 := strconv.ParseFloat(fmt.Sprint(settings[prefix+"longitude"]), 64)
			if e1 != nil || e2 != nil {
				continue
			}
			label, _ := settings[prefix+"label"].(string)
			stop, err := newStop(StopInput{lat, lon, label})
			if err != nil {
				continue
			}
			stop.Reached = fmt.Sprint(settings[prefix+"reached"]) == "true"
			s.plan.Stops = append(s.plan.Stops, stop)
		}
		if len(s.plan.Stops) > 0 {
			step, _ := strconv.Atoi(fmt.Sprint(settings["dashboard.route-plan.current-step"]))
			if step >= 0 && step < len(s.plan.Stops) {
				s.plan.CurrentStep = step
			}
		}
	} else if fmt.Sprint(settings["dashboard.route-plan.active"]) == "false" {
		return nil
	} else {
		nav, err := s.ipc.HGetAll("navigation")
		if err != nil {
			return err
		}
		var inputs []StopInput
		if nav["waypoints"] != "" {
			if err := json.Unmarshal([]byte(nav["waypoints"]), &inputs); err != nil {
				return fmt.Errorf("migrate navigation waypoints: %w", err)
			}
		} else if nav["latitude"] != "" && nav["longitude"] != "" {
			lat, e1 := strconv.ParseFloat(nav["latitude"], 64)
			lon, e2 := strconv.ParseFloat(nav["longitude"], 64)
			if e1 != nil || e2 != nil {
				return errors.New("invalid legacy destination")
			}
			inputs = []StopInput{{lat, lon, nav["address"]}}
		}
		for _, input := range inputs {
			stop, err := newStop(input)
			if err != nil {
				return err
			}
			s.plan.Stops = append(s.plan.Stops, stop)
		}
		if len(s.plan.Stops) > 0 {
			step, _ := strconv.Atoi(nav["current-step"])
			if step >= 0 && step < len(s.plan.Stops) {
				s.plan.CurrentStep = step
			}
		}
	}
	if len(s.plan.Stops) > 32 {
		return errors.New("legacy route plan exceeds 32 stops")
	}
	if len(s.plan.Stops) > 0 {
		id, err := destination.NewUUID()
		if err != nil {
			return err
		}
		s.plan.ID = id
		s.plan.Revision = 1
	}
	return nil
}
