package api

import (
	"context"
	"testing"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/runtime"
)

// startRigAgentSessions starts a runtime session for every configured agent of
// rig, so fixtures can write session metadata the way the tmux provider does:
// only for sessions that exist.
func startRigAgentSessions(t *testing.T, state *fakeMutatorState, rig string) {
	t.Helper()
	cfg := state.Config()
	tmpl := cfg.Workspace.SessionTemplate
	for _, a := range cfg.Agents {
		if a.Dir != rig {
			continue
		}
		for _, ea := range expandAgent(a, state.cityName, tmpl, state.sp) {
			name := agent.SessionNameFor(state.cityName, ea.qualifiedName, tmpl)
			if err := state.sp.Start(context.Background(), name, runtime.Config{}); err != nil {
				t.Fatalf("Start %s: %v", name, err)
			}
		}
	}
}
