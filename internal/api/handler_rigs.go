package api

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	gitpkg "github.com/gastownhall/gascity/internal/git"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/suspensionstate"
	workdirutil "github.com/gastownhall/gascity/internal/workdir"
)

type rigResponse struct {
	Name          string     `json:"name"`
	Path          string     `json:"path"`
	Suspended     bool       `json:"suspended"`
	Prefix        string     `json:"prefix,omitempty"`
	DefaultBranch string     `json:"default_branch,omitempty"`
	AgentCount    int        `json:"agent_count"`
	RunningCount  int        `json:"running_count"`
	LastActivity  *time.Time `json:"last_activity,omitempty"`
	Git           *gitStatus `json:"git,omitempty"`
}

type gitStatus struct {
	Branch       string `json:"branch"`
	Clean        bool   `json:"clean"`
	ChangedFiles int    `json:"changed_files"`
	Ahead        int    `json:"ahead"`
	Behind       int    `json:"behind"`
}

// rigAgentSession is one configured agent session as one request sees it.
type rigAgentSession struct {
	sessionName  string
	exists       bool
	running      bool
	lastActivity *time.Time
}

// rigSessionSnapshot holds one request's configured agent sessions grouped by
// rig. Each session is observed once, so the rig counts, the activity and the
// suspended state all derive from the same reads rather than repeating them
// per rig.
type rigSessionSnapshot struct {
	sp     runtime.Provider
	byRig  map[string][]rigAgentSession
	listed sessionListing
}

// snapshotRigSessions observes the configured agent sessions of rig only, or of
// every rig when only is empty. The session listing is read once per call.
func snapshotRigSessions(cfg *config.City, sp runtime.Provider, cityName, cityPath, only string) rigSessionSnapshot {
	tmpl := cfg.Workspace.SessionTemplate
	listed := listRuntimeSessions(sp)
	snap := rigSessionSnapshot{
		sp:     sp,
		byRig:  make(map[string][]rigAgentSession),
		listed: listed,
	}
	for _, a := range cfg.Agents {
		rigName := workdirutil.ConfiguredRigName(cityPath, a, cfg.Rigs)
		if rigName == "" || (only != "" && rigName != only) {
			continue
		}
		processNames := config.AgentProcessNames(cfg, a, exec.LookPath)
		for _, ea := range expandAgent(a, cityName, tmpl, sp) {
			sessionName := agent.SessionNameFor(cityName, ea.qualifiedName, tmpl)
			snap.byRig[rigName] = append(snap.byRig[rigName], observeRigAgentSession(sp, sessionName, processNames, listed))
		}
	}
	return snap
}

// observeRigAgentSession reads liveness for one session, which is cached by the
// runtime provider. Only a running session pays for an activity read.
func observeRigAgentSession(sp runtime.Provider, sessionName string, processNames []string, listed sessionListing) rigAgentSession {
	sess := rigAgentSession{
		sessionName: sessionName,
		exists:      listed.mayExist(sessionName),
	}
	sess.running = runtime.ObserveLiveness(sp, sessionName, processNames).Running
	if !sess.running {
		return sess
	}
	if lastActive, err := sp.GetLastActivity(sessionName); err == nil && !lastActive.IsZero() {
		last := lastActive
		sess.lastActivity = &last
	}
	return sess
}

// buildRigResponse creates a rigResponse with agent counts and last activity
// from the request's session snapshot.
func (s *Server) buildRigResponse(rig config.Rig, snap rigSessionSnapshot, cityPath string) rigResponse {
	sessions := snap.byRig[rig.Name]
	var runningCount int
	var maxActivity time.Time
	for _, sess := range sessions {
		if sess.running {
			runningCount++
		}
		if sess.lastActivity != nil && sess.lastActivity.After(maxActivity) {
			maxActivity = *sess.lastActivity
		}
	}

	resp := rigResponse{
		Name:          rig.Name,
		Path:          rig.Path,
		Suspended:     rigSuspended(rig, sessions, snap.sp, cityPath),
		Prefix:        rig.Prefix,
		DefaultBranch: rig.DefaultBranch,
		AgentCount:    len(sessions),
		RunningCount:  runningCount,
	}
	if !maxActivity.IsZero() {
		resp.LastActivity = &maxActivity
	}
	return resp
}

// rigSuspended computes the effective suspended state for a rig. A rig
// is suspended if the runtime state file records an explicit "suspended"
// preference, if the rig's SuspendedOnStart applies with no overriding
// runtime entry, or if all its agents are runtime-suspended via session
// metadata. The deprecated `[[rig]] suspended` field in city.toml is
// intentionally NOT consulted — `gc doctor` surfaces it as a migration
// target.
func rigSuspended(rig config.Rig, sessions []rigAgentSession, sp runtime.Provider, cityPath string) bool {
	if rs, err := suspensionstate.Load(fsys.OSFS{}, cityPath); err == nil &&
		suspensionstate.EffectiveRigSuspended(rs, rig.Name, rig.EffectiveSuspendedOnStart()) {
		return true
	}
	if len(sessions) == 0 {
		return false
	}
	// Suspended metadata lives on the session itself, so a rig with any
	// session missing cannot be suspended by metadata. Checking every
	// session's existence first skips the metadata reads in that case.
	for _, sess := range sessions {
		if !sess.exists {
			return false
		}
	}
	for _, sess := range sessions {
		if !sessionMetadataIsTrue(sp, sess.sessionName, "suspended") {
			return false
		}
	}
	return true
}

// sessionMetadataIsTrue reports whether the session metadata key is "true".
// A read error reads as false, matching an unset key.
func sessionMetadataIsTrue(sp runtime.Provider, sessionName, key string) bool {
	value, err := sp.GetMeta(sessionName, key)
	return err == nil && strings.TrimSpace(value) == "true"
}

// gitStatusTimeout bounds how long git operations can take per rig.
const gitStatusTimeout = 3 * time.Second

// fetchGitStatus uses internal/git to get branch/status/ahead-behind info.
// Returns nil on any error or timeout (rig may not be a git repo).
// The context-based timeout ensures that git subprocesses are killed on
// expiry, preventing goroutine and process leaks.
func fetchGitStatus(path string) *gitStatus {
	ctx, cancel := context.WithTimeout(context.Background(), gitStatusTimeout)
	defer cancel()
	return fetchGitStatusCtx(ctx, path)
}

func fetchGitStatusCtx(ctx context.Context, path string) *gitStatus {
	g := gitpkg.New(path)
	if !g.IsRepoCtx(ctx) {
		return nil
	}

	branch, err := g.CurrentBranchCtx(ctx)
	if err != nil {
		return nil
	}

	porcelain, err := g.StatusPorcelainCtx(ctx)
	if err != nil {
		return nil
	}

	var changedFiles int
	for _, line := range strings.Split(porcelain, "\n") {
		if strings.TrimSpace(line) != "" {
			changedFiles++
		}
	}

	gs := &gitStatus{
		Branch:       branch,
		Clean:        changedFiles == 0,
		ChangedFiles: changedFiles,
	}

	// Ahead/behind (best-effort — fails if no upstream set).
	ahead, behind, err := g.AheadBehindCtx(ctx)
	if err == nil {
		gs.Ahead = ahead
		gs.Behind = behind
	}

	return gs
}
