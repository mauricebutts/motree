package state

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Repo struct {
	RepoPath     string `json:"repo_path"`
	WorktreePath string `json:"worktree_path"`
}

type Session struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	LastOpened  int64  `json:"last_opened"`
	Repos       []Repo `json:"repos"`
}

type State struct {
	Sessions []Session `json:"sessions"`
}

func StateFilePath() string {
	if path := os.Getenv("MOTREE_STATE_FILE"); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".motree"
	}
	return filepath.Join(home, ".motree")
}

func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{}, nil
		}
		return nil, err
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}

	return &s, nil
}

func (s *State) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	// Write to temp file first with unique random suffix
	tmpPath := path + ".tmp." + fmt.Sprintf("%d%d", time.Now().UnixNano(), rand.Intn(1000000))
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		// Best effort cleanup
		_ = os.Remove(tmpPath)
		return err
	}

	return nil
}

func (s *State) UpsertSession(name, description string, lastOpened int64) {
	for i, sess := range s.Sessions {
		if sess.Name == name {
			s.Sessions[i].LastOpened = lastOpened
			if description != "" {
				s.Sessions[i].Description = description
			}
			return
		}
	}

	// Not found, insert new
	s.Sessions = append(s.Sessions, Session{
		Name:        name,
		Description: description,
		LastOpened:  lastOpened,
		Repos:       []Repo{},
	})
}

func (s *State) EnsureRepo(sessionName, repoPath, worktreePath string) {
	for i, sess := range s.Sessions {
		if sess.Name == sessionName {
			// Check if repo already exists
			for _, repo := range sess.Repos {
				if repo.RepoPath == repoPath {
					return // Already exists, no-op
				}
			}
			// Add new repo
			s.Sessions[i].Repos = append(s.Sessions[i].Repos, Repo{
				RepoPath:     repoPath,
				WorktreePath: worktreePath,
			})
			return
		}
	}
}

// ReposFor returns a copy of sessionName's repos, never the session's own
// backing slice. The copy is load-bearing: RemoveRepo rewrites that slice
// in place, so a caller ranging over the result while pruning as it goes
// (mtcmd.AttachOrRecreate does exactly that) would otherwise have the
// iteration shift underneath it and silently skip repos.
func (s *State) ReposFor(sessionName string) []Repo {
	for _, sess := range s.Sessions {
		if sess.Name == sessionName {
			return append([]Repo(nil), sess.Repos...)
		}
	}
	return []Repo{}
}

func (s *State) RemoveRepo(sessionName, repoPath string) {
	for i, sess := range s.Sessions {
		if sess.Name == sessionName {
			// Find and remove the repo
			for j, repo := range sess.Repos {
				if repo.RepoPath == repoPath {
					s.Sessions[i].Repos = append(sess.Repos[:j], sess.Repos[j+1:]...)
					return
				}
			}
			return
		}
	}
}

func (s *State) RemoveSession(name string) {
	for i, sess := range s.Sessions {
		if sess.Name == name {
			s.Sessions = append(s.Sessions[:i], s.Sessions[i+1:]...)
			return
		}
	}
}

func (s *State) SortedSessions() []Session {
	// Create a copy to avoid modifying the original
	sessions := make([]Session, len(s.Sessions))
	copy(sessions, s.Sessions)

	sort.Slice(sessions, func(i, j int) bool {
		// Sort by LastOpened descending
		// But 0 timestamps should sort last
		iLastOpened := sessions[i].LastOpened
		jLastOpened := sessions[j].LastOpened

		// If both are 0, maintain original order
		if iLastOpened == 0 && jLastOpened == 0 {
			return false
		}
		// If only i is 0, j comes first
		if iLastOpened == 0 {
			return false
		}
		// If only j is 0, i comes first
		if jLastOpened == 0 {
			return true
		}
		// Both non-zero, sort descending
		return iLastOpened > jLastOpened
	})

	return sessions
}
