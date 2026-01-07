package session

import (
	"encoding/json"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"dv/internal/xdg"
)

// State holds per-terminal session selections.
type State struct {
	Sessions map[int]string `json:"sessions"` // SID -> agent name
}

func sessionsPath() (string, error) {
	runtimeDir, err := xdg.RuntimeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(runtimeDir, "sessions.json"), nil
}

// CurrentSID returns the session ID of the current terminal.
func CurrentSID() (int, error) {
	return unix.Getsid(0)
}

func processExists(pid int) bool {
	// kill with signal 0 checks if process exists without sending signal
	err := unix.Kill(pid, 0)
	return err == nil
}

// Load reads the session state from disk.
func Load() (*State, error) {
	path, err := sessionsPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &State{Sessions: make(map[int]string)}, nil
	}
	if err != nil {
		return nil, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return &State{Sessions: make(map[int]string)}, nil
	}
	if state.Sessions == nil {
		state.Sessions = make(map[int]string)
	}
	return &state, nil
}

// Save writes the session state to disk, cleaning stale entries.
func (s *State) Save() error {
	path, err := sessionsPath()
	if err != nil {
		return err
	}

	// Clean stale entries while saving
	for sid := range s.Sessions {
		if !processExists(sid) {
			delete(s.Sessions, sid)
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// Get returns the agent for the given session ID, or empty if not found/stale.
func (s *State) Get(sid int) string {
	if !processExists(sid) {
		delete(s.Sessions, sid)
		return ""
	}
	return s.Sessions[sid]
}

// Set stores the agent for the given session ID.
func (s *State) Set(sid int, agent string) {
	s.Sessions[sid] = agent
}

// GetCurrentAgent returns the agent for the current terminal session.
func GetCurrentAgent() string {
	sid, err := CurrentSID()
	if err != nil {
		return ""
	}
	state, err := Load()
	if err != nil {
		return ""
	}
	return state.Get(sid)
}

// SetCurrentAgent sets the agent for the current terminal session.
func SetCurrentAgent(agent string) error {
	sid, err := CurrentSID()
	if err != nil {
		return err
	}
	state, err := Load()
	if err != nil {
		return err
	}
	state.Set(sid, agent)
	return state.Save()
}
