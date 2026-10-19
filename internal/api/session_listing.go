package api

import (
	"log"

	"github.com/gastownhall/gascity/internal/runtime"
)

// sessionListing is one request's view of which runtime sessions exist. A
// session absent from a successful listing has no environment to read, so
// callers skip its per-session metadata reads, which fork a process each
// time for tmux. A failed listing is unknown and reports every session as
// possibly present, so callers fall back to reading each session.
type sessionListing struct {
	names map[string]struct{}
	known bool
}

// listRuntimeSessions lists every runtime session once. The listing is
// unknown when the provider is nil or the listing fails; the failure is
// logged because the fallback reads each session.
func listRuntimeSessions(sp runtime.Provider) sessionListing {
	if sp == nil {
		return sessionListing{}
	}
	names, err := sp.ListRunning("")
	if err != nil {
		log.Printf("api: listing runtime sessions failed, reading per-session metadata instead: %v", err)
		return sessionListing{}
	}
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return sessionListing{names: set, known: true}
}

// mayExist reports whether sessionName can have metadata. It is true when the
// listing is unknown, because absence cannot then be trusted.
func (l sessionListing) mayExist(sessionName string) bool {
	if !l.known {
		return true
	}
	_, ok := l.names[sessionName]
	return ok
}
