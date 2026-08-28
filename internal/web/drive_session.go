package web

import (
	"sync"

	"github.com/johnpostlethwait/bluforge/internal/discdb"
)

// DriveSession holds transient per-drive workflow state: the user's selected
// release from TheDiscDB and cached search results. This state persists across
// browser refreshes, and belongs to the disc it was created for.
type DriveSession struct {
	// DiscLabel is the volume label of the disc that was in the drive when this
	// session was created — the physical disc, not TheDiscDB's DiscID below.
	//
	// A selection describes one disc, and nothing used to tie it to that disc:
	// swapping discs left the previous one's match applying to whatever came
	// next. Both events meant to prevent that miss an ordinary swap, so the
	// binding is recorded here and checked against the drive on every render.
	// Empty on a session created before this field existed, which cannot be
	// proven stale and is therefore kept.
	DiscLabel         string
	MediaItemID       string
	ReleaseID         string
	DiscID            string
	MediaTitle        string
	MediaYear         string
	MediaType         string
	ReleaseUPC        string
	ReleaseASIN       string
	ReleaseRegionCode string
	ReleaseLocale     string
	SearchResults     []SearchResultJSON
	RawSearchResults  []discdb.MediaItem
}

// DriveSessionStore is a thread-safe map of drive index to session state.
type DriveSessionStore struct {
	mu       sync.RWMutex
	sessions map[int]*DriveSession
}

// NewDriveSessionStore creates an empty session store.
func NewDriveSessionStore() *DriveSessionStore {
	return &DriveSessionStore{
		sessions: make(map[int]*DriveSession),
	}
}

// Get returns the session for the given drive index, or nil if none exists.
func (s *DriveSessionStore) Get(driveIndex int) *DriveSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[driveIndex]
}

// Set stores a session for the given drive index.
func (s *DriveSessionStore) Set(driveIndex int, session *DriveSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[driveIndex] = session
}

// Clear removes the session for the given drive index.
func (s *DriveSessionStore) Clear(driveIndex int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, driveIndex)
}

// SetSearchResults stores search results for the given drive index.
// Creates a new session if one does not exist.
func (s *DriveSessionStore) SetSearchResults(driveIndex int, results []SearchResultJSON) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[driveIndex]
	if !ok {
		session = &DriveSession{}
		s.sessions[driveIndex] = session
	}
	session.SearchResults = results
}

// SetRawSearchResults stores raw MediaItem search results for the given drive index.
// Creates a new session if one does not exist.
func (s *DriveSessionStore) SetRawSearchResults(driveIndex int, items []discdb.MediaItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[driveIndex]
	if !ok {
		session = &DriveSession{}
		s.sessions[driveIndex] = session
	}
	session.RawSearchResults = items
}

// SetDiscLabel records which disc a session belongs to, creating the session if
// it does not exist. A session created by a search rather than a selection has
// no label of its own, and without one it cannot be recognised as stale after a
// disc swap.
func (s *DriveSessionStore) SetDiscLabel(driveIndex int, label string) {
	if label == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[driveIndex]
	if !ok {
		session = &DriveSession{}
		s.sessions[driveIndex] = session
	}
	session.DiscLabel = label
}
