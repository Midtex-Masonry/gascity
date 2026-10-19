package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

// countSessionCalls counts recorded provider calls with the given method.
func countSessionCalls(calls []runtime.Call, method string) int {
	n := 0
	for _, c := range calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

// TestRigListReadsSessionMetadataOnlyForExistingSessions pins the /rigs fork
// budget: one session listing per request, one activity read per running
// agent, and no environment reads for agents whose tmux session does not exist.
func TestRigListReadsSessionMetadataOnlyForExistingSessions(t *testing.T) {
	state := newFakeState(t)
	state.cfg.Rigs = append(state.cfg.Rigs, config.Rig{Name: "other", Path: "/tmp/other"})
	state.cfg.Agents = []config.Agent{
		{Name: "w1", Dir: "myrig", MaxActiveSessions: intPtr(1)},
		{Name: "w2", Dir: "myrig", MaxActiveSessions: intPtr(1)},
		{Name: "w3", Dir: "myrig", MaxActiveSessions: intPtr(1)},
		{Name: "w4", Dir: "myrig", MaxActiveSessions: intPtr(1)},
		{Name: "o1", Dir: "other", MaxActiveSessions: intPtr(1)},
		{Name: "o2", Dir: "other", MaxActiveSessions: intPtr(1)},
	}
	sp := runtime.NewFake()
	for _, name := range []string{"myrig--w1", "other--o1"} {
		if err := sp.Start(context.Background(), name, runtime.Config{}); err != nil {
			t.Fatalf("Start %s: %v", name, err)
		}
	}
	state.sp = sp
	h := newTestCityHandler(t, state)

	sp.Calls = nil
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", cityURL(state, "/rigs"), nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Items []rigResponse `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	running := 0
	for _, rig := range body.Items {
		running += rig.RunningCount
		switch rig.Name {
		case "myrig":
			if rig.AgentCount != 4 || rig.RunningCount != 1 {
				t.Errorf("myrig agents=%d running=%d, want 4 and 1", rig.AgentCount, rig.RunningCount)
			}
		case "other":
			if rig.AgentCount != 2 || rig.RunningCount != 1 {
				t.Errorf("other agents=%d running=%d, want 2 and 1", rig.AgentCount, rig.RunningCount)
			}
		}
	}
	if running != 2 {
		t.Fatalf("total running = %d, want 2", running)
	}

	if got := countSessionCalls(sp.Calls, "GetMeta"); got != 0 {
		t.Errorf("GetMeta calls = %d, want 0 when no rig has every agent running", got)
	}
	if got := countSessionCalls(sp.Calls, "ListRunning"); got > 1 {
		t.Errorf("ListRunning calls = %d, want at most 1 per request", got)
	}
	forks := countSessionCalls(sp.Calls, "ListRunning") + countSessionCalls(sp.Calls, "GetMeta") + countSessionCalls(sp.Calls, "GetLastActivity")
	if forks > 1+running {
		t.Errorf("session provider calls = %d, want at most 1 + %d running sessions", forks, running)
	}
}

// TestRigSuspendedReadsMetadataOncePerAgent pins that a rig whose agents all
// run reads each agent's suspended metadata exactly once per request, and that
// the suspended result is still derived from those reads.
func TestRigSuspendedReadsMetadataOncePerAgent(t *testing.T) {
	state := newFakeState(t)
	state.cfg.Rigs = append(state.cfg.Rigs, config.Rig{Name: "other", Path: "/tmp/other"})
	state.cfg.Agents = []config.Agent{
		{Name: "o1", Dir: "other", MaxActiveSessions: intPtr(1)},
		{Name: "o2", Dir: "other", MaxActiveSessions: intPtr(1)},
	}
	sp := runtime.NewFake()
	for _, name := range []string{"other--o1", "other--o2"} {
		if err := sp.Start(context.Background(), name, runtime.Config{}); err != nil {
			t.Fatalf("Start %s: %v", name, err)
		}
		if err := sp.SetMeta(name, "suspended", "true"); err != nil {
			t.Fatalf("SetMeta %s: %v", name, err)
		}
	}
	state.sp = sp
	h := newTestCityHandler(t, state)

	sp.Calls = nil
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", cityURL(state, "/rig/other"), nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var rig rigResponse
	if err := json.NewDecoder(rec.Body).Decode(&rig); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !rig.Suspended {
		t.Errorf("Suspended = false, want true when every agent session is suspended")
	}
	if got := countSessionCalls(sp.Calls, "GetMeta"); got != 2 {
		t.Errorf("suspended metadata reads = %d, want exactly one per agent (2)", got)
	}
}

// TestAgentListReadsSuspendedMetadataOnlyForExistingSessions pins the /agents
// fork budget: a configured agent with no tmux session needs no metadata read.
func TestAgentListReadsSuspendedMetadataOnlyForExistingSessions(t *testing.T) {
	state := newFakeState(t)
	state.cfg.Agents = []config.Agent{
		{Name: "w1", Dir: "myrig", MaxActiveSessions: intPtr(1)},
		{Name: "w2", Dir: "myrig", MaxActiveSessions: intPtr(1)},
		{Name: "w3", Dir: "myrig", MaxActiveSessions: intPtr(1)},
	}
	sp := runtime.NewFake()
	if err := sp.Start(context.Background(), "myrig--w1", runtime.Config{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	state.sp = sp
	h := newTestCityHandler(t, state)

	sp.Calls = nil
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", cityURL(state, "/agents"), nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := countSessionCalls(sp.Calls, "GetMeta"); got > 2 {
		t.Errorf("GetMeta calls = %d, want at most 2: suspended and GC_SESSION_ID for the one running agent only", got)
	}
}
