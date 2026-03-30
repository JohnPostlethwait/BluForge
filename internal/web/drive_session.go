package web

import (
	"sync"
	"time"
)

// sessionTTL is the maximum age of an idle drive session before it is expired.
const sessionTTL = 4 * time.Hour

// DriveSession holds transient per-drive workflow state: the user's selected
// release from TheDiscDB and cached search results. This state persists across
// browser refreshes but is cleared when the disc is ejected or the TTL expires.
type DriveSession struct {
	MediaItemID   string
	ReleaseID     string
	MediaTitle    string
	MediaYear     string
	MediaType     string
	SearchResults []SearchResultJSON
	lastAccess    time.Time
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

// Get returns the session for the given drive index, or nil if none exists
// or the session has expired.
func (s *DriveSessionStore) Get(driveIndex int) *DriveSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[driveIndex]
	if !ok {
		return nil
	}
	if time.Since(sess.lastAccess) > sessionTTL {
		delete(s.sessions, driveIndex)
		return nil
	}
	sess.lastAccess = time.Now()
	return sess
}

// Set stores a session for the given drive index.
func (s *DriveSessionStore) Set(driveIndex int, session *DriveSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session.lastAccess = time.Now()
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
	session.lastAccess = time.Now()
}
