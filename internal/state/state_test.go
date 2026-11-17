package state

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStateFilePath_DefaultsToHomeMotree(t *testing.T) {
	oldEnv, exists := os.LookupEnv("MOTREE_STATE_FILE")
	if exists {
		os.Unsetenv("MOTREE_STATE_FILE")
		defer os.Setenv("MOTREE_STATE_FILE", oldEnv)
	}

	got := StateFilePath()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".motree")

	if got != want {
		t.Errorf("StateFilePath() = %q, want %q", got, want)
	}
}

func TestStateFilePath_HonorsEnvOverride(t *testing.T) {
	oldEnv, exists := os.LookupEnv("MOTREE_STATE_FILE")
	defer func() {
		if exists {
			os.Setenv("MOTREE_STATE_FILE", oldEnv)
		} else {
			os.Unsetenv("MOTREE_STATE_FILE")
		}
	}()

	want := "/tmp/my_state_file"
	os.Setenv("MOTREE_STATE_FILE", want)

	got := StateFilePath()
	if got != want {
		t.Errorf("StateFilePath() = %q, want %q", got, want)
	}
}

func TestLoad_MissingFile_ReturnsEmptyState(t *testing.T) {
	s, err := Load("/nonexistent/file/path")
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if s == nil {
		t.Fatal("Load() returned nil, want *State")
	}
	if len(s.Sessions) != 0 {
		t.Errorf("Load() empty state has %d sessions, want 0", len(s.Sessions))
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.json")

	original := &State{
		Sessions: []Session{
			{
				Name:        "session1",
				Description: "my first session",
				LastOpened:  1234567890,
				Repos: []Repo{
					{RepoPath: "/path/to/repo1", WorktreePath: "/path/to/wt1"},
					{RepoPath: "/path/to/repo2", WorktreePath: "/path/to/wt2"},
				},
			},
			{
				Name:        "session2",
				Description: "another session",
				LastOpened:  1234567900,
				Repos:       []Repo{},
			},
		},
	}

	if err := original.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(loaded.Sessions) != len(original.Sessions) {
		t.Errorf("loaded sessions count = %d, want %d", len(loaded.Sessions), len(original.Sessions))
	}

	for i, sess := range loaded.Sessions {
		if sess.Name != original.Sessions[i].Name {
			t.Errorf("session[%d].Name = %q, want %q", i, sess.Name, original.Sessions[i].Name)
		}
		if sess.Description != original.Sessions[i].Description {
			t.Errorf("session[%d].Description = %q, want %q", i, sess.Description, original.Sessions[i].Description)
		}
		if sess.LastOpened != original.Sessions[i].LastOpened {
			t.Errorf("session[%d].LastOpened = %d, want %d", i, sess.LastOpened, original.Sessions[i].LastOpened)
		}
		if len(sess.Repos) != len(original.Sessions[i].Repos) {
			t.Errorf("session[%d] repos count = %d, want %d", i, len(sess.Repos), len(original.Sessions[i].Repos))
		}
	}
}

func TestUpsertSession_NewAndUpdatePreservingDescriptionWhenEmpty(t *testing.T) {
	s := &State{}

	// Insert new session
	s.UpsertSession("session1", "first description", 1000)
	if len(s.Sessions) != 1 {
		t.Fatalf("after insert: got %d sessions, want 1", len(s.Sessions))
	}
	if s.Sessions[0].Name != "session1" {
		t.Errorf("session name = %q, want %q", s.Sessions[0].Name, "session1")
	}
	if s.Sessions[0].Description != "first description" {
		t.Errorf("session description = %q, want %q", s.Sessions[0].Description, "first description")
	}
	if s.Sessions[0].LastOpened != 1000 {
		t.Errorf("session LastOpened = %d, want 1000", s.Sessions[0].LastOpened)
	}

	// Update with new description
	s.UpsertSession("session1", "second description", 2000)
	if len(s.Sessions) != 1 {
		t.Fatalf("after update: got %d sessions, want 1", len(s.Sessions))
	}
	if s.Sessions[0].Description != "second description" {
		t.Errorf("after update: description = %q, want %q", s.Sessions[0].Description, "second description")
	}
	if s.Sessions[0].LastOpened != 2000 {
		t.Errorf("after update: LastOpened = %d, want 2000", s.Sessions[0].LastOpened)
	}

	// Update with empty description should preserve existing
	s.UpsertSession("session1", "", 3000)
	if s.Sessions[0].Description != "second description" {
		t.Errorf("empty description should preserve: got %q, want %q", s.Sessions[0].Description, "second description")
	}
	if s.Sessions[0].LastOpened != 3000 {
		t.Errorf("LastOpened should update: got %d, want 3000", s.Sessions[0].LastOpened)
	}
}

func TestEnsureRepo_NoDuplicateOnRerun(t *testing.T) {
	s := &State{}
	s.UpsertSession("session1", "desc", 100)

	s.EnsureRepo("session1", "/repo1", "/wt1")
	if len(s.Sessions[0].Repos) != 1 {
		t.Errorf("after first ensure: got %d repos, want 1", len(s.Sessions[0].Repos))
	}

	// Ensure same repo again - should be no-op
	s.EnsureRepo("session1", "/repo1", "/wt1_different")
	if len(s.Sessions[0].Repos) != 1 {
		t.Errorf("after re-ensure: got %d repos, want 1 (no duplicate)", len(s.Sessions[0].Repos))
	}
	if s.Sessions[0].Repos[0].WorktreePath != "/wt1" {
		t.Errorf("worktree path should not change: got %q, want %q", s.Sessions[0].Repos[0].WorktreePath, "/wt1")
	}

	// Ensure different repo - should add
	s.EnsureRepo("session1", "/repo2", "/wt2")
	if len(s.Sessions[0].Repos) != 2 {
		t.Errorf("after adding different repo: got %d repos, want 2", len(s.Sessions[0].Repos))
	}
}

func TestRemoveRepo_DropsOnlyThatRow(t *testing.T) {
	s := &State{}
	s.UpsertSession("session1", "desc", 100)
	s.EnsureRepo("session1", "/repo1", "/wt1")
	s.EnsureRepo("session1", "/repo2", "/wt2")
	s.EnsureRepo("session1", "/repo3", "/wt3")

	s.RemoveRepo("session1", "/repo2")
	if len(s.Sessions[0].Repos) != 2 {
		t.Errorf("after remove: got %d repos, want 2", len(s.Sessions[0].Repos))
	}

	// Check that repo2 is gone and others remain
	for _, r := range s.Sessions[0].Repos {
		if r.RepoPath == "/repo2" {
			t.Error("RemoveRepo did not remove /repo2")
		}
	}

	if s.Sessions[0].Repos[0].RepoPath != "/repo1" {
		t.Errorf("first repo = %q, want /repo1", s.Sessions[0].Repos[0].RepoPath)
	}
	if s.Sessions[0].Repos[1].RepoPath != "/repo3" {
		t.Errorf("second repo = %q, want /repo3", s.Sessions[0].Repos[1].RepoPath)
	}
}

func TestSortedSessions_MostRecentFirst_ZeroTimestampLast(t *testing.T) {
	s := &State{
		Sessions: []Session{
			{Name: "old", LastOpened: 1000},
			{Name: "newest", LastOpened: 3000},
			{Name: "unknown", LastOpened: 0},
			{Name: "middle", LastOpened: 2000},
		},
	}

	sorted := s.SortedSessions()
	if len(sorted) != 4 {
		t.Fatalf("got %d sessions, want 4", len(sorted))
	}

	// Should be: newest (3000), middle (2000), old (1000), unknown (0)
	wantOrder := []string{"newest", "middle", "old", "unknown"}
	for i, name := range wantOrder {
		if sorted[i].Name != name {
			t.Errorf("sorted[%d].Name = %q, want %q", i, sorted[i].Name, name)
		}
	}
}

func TestSave_ConcurrentCallsNeverProduceInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "concurrent_state.json")

	// Create a few distinct states
	states := make([]*State, 5)
	for i := 0; i < 5; i++ {
		s := &State{
			Sessions: []Session{
				{
					Name:        "session_" + string(rune(i+'0')),
					Description: "desc",
					LastOpened:  int64(1000 + i),
				},
			},
		}
		states[i] = s
	}

	// Launch concurrent saves
	done := make(chan error, len(states))
	for i := 0; i < len(states); i++ {
		go func(idx int) {
			done <- states[idx].Save(path)
		}(i)
	}

	// Wait for all to complete
	for i := 0; i < len(states); i++ {
		err := <-done
		if err != nil {
			t.Errorf("Save() error = %v", err)
		}
	}

	// Load and verify the final state is valid JSON
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() after concurrent saves error = %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() returned nil")
	}
}

func TestReposFor_ReturnsReposForSession(t *testing.T) {
	s := &State{}
	s.UpsertSession("session1", "desc", 100)
	s.EnsureRepo("session1", "/repo1", "/wt1")
	s.EnsureRepo("session1", "/repo2", "/wt2")

	repos := s.ReposFor("session1")
	if len(repos) != 2 {
		t.Errorf("ReposFor() got %d repos, want 2", len(repos))
	}

	repos2 := s.ReposFor("nonexistent")
	if len(repos2) != 0 {
		t.Errorf("ReposFor() nonexistent got %d repos, want 0", len(repos2))
	}
}

func TestRemoveSession(t *testing.T) {
	s := &State{}
	s.UpsertSession("session1", "desc1", 100)
	s.UpsertSession("session2", "desc2", 200)

	s.RemoveSession("session1")
	if len(s.Sessions) != 1 {
		t.Fatalf("after remove: got %d sessions, want 1", len(s.Sessions))
	}
	if s.Sessions[0].Name != "session2" {
		t.Errorf("remaining session name = %q, want %q", s.Sessions[0].Name, "session2")
	}
}

func TestIsTracked_TrueForExistingSession(t *testing.T) {
	s := &State{}
	s.UpsertSession("session1", "desc1", 100)

	if !s.IsTracked("session1") {
		t.Errorf("IsTracked(%q) = false, want true", "session1")
	}
}

func TestIsTracked_FalseForUnknownSession(t *testing.T) {
	s := &State{}
	s.UpsertSession("session1", "desc1", 100)

	if s.IsTracked("nonexistent") {
		t.Errorf("IsTracked(%q) = true, want false", "nonexistent")
	}
}

func TestEnsureRepoPR_RecordsPRNumberOnTheRepoRow(t *testing.T) {
	s := &State{}
	s.UpsertSession("review-care-spike", "PR #420", 1)

	s.EnsureRepoPR("review-care-spike", "/repo1", "/wt1", "420")

	want := []Repo{{RepoPath: "/repo1", WorktreePath: "/wt1", PRNumber: "420"}}
	if got := s.ReposFor("review-care-spike"); !reflect.DeepEqual(got, want) {
		t.Errorf("ReposFor = %+v, want %+v", got, want)
	}
}

func TestSessionForPR_FindsSessionHoldingThatRepoAndPR(t *testing.T) {
	s := &State{}
	s.UpsertSession("other", "d", 1)
	s.EnsureRepoPR("other", "/repo1", "/wt-other", "77")
	s.UpsertSession("review-care-spike", "PR #420", 1)
	s.EnsureRepoPR("review-care-spike", "/repo1", "/wt1", "420")

	if got := s.SessionForPR("/repo1", "420"); got != "review-care-spike" {
		t.Errorf("SessionForPR = %q, want %q", got, "review-care-spike")
	}
}

func TestSessionForPR_SamePRNumberInAnotherRepoDoesNotMatch(t *testing.T) {
	s := &State{}
	s.UpsertSession("a", "d", 1)
	s.EnsureRepoPR("a", "/repo1", "/wt1", "420")

	// ~/.motree is global across repos, so PR 420 in /repo2 is a different
	// thing entirely and must not collide with PR 420 in /repo1.
	if got := s.SessionForPR("/repo2", "420"); got != "" {
		t.Errorf("SessionForPR = %q, want empty string", got)
	}
}

func TestSessionForPR_IgnoresRowsWithNoPRNumber(t *testing.T) {
	s := &State{}
	s.UpsertSession("legacy", "d", 1)
	s.EnsureRepo("legacy", "/repo1", "/wt1") // how older builds wrote every row

	if got := s.SessionForPR("/repo1", "420"); got != "" {
		t.Errorf("SessionForPR = %q, want empty string", got)
	}
}

func TestSessionForPR_EmptyPRNumberNeverMatches(t *testing.T) {
	s := &State{}
	s.UpsertSession("legacy", "d", 1)
	s.EnsureRepo("legacy", "/repo1", "/wt1")

	// Without this guard every PR-less row would match an empty lookup.
	if got := s.SessionForPR("/repo1", ""); got != "" {
		t.Errorf("SessionForPR = %q, want empty string", got)
	}
}

func TestSaveLoad_RoundTripPreservesPRNumber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	original := &State{
		Sessions: []Session{{
			Name:        "review-care-spike",
			Description: "PR #420",
			LastOpened:  1234567890,
			Repos:       []Repo{{RepoPath: "/repo1", WorktreePath: "/wt1", PRNumber: "420"}},
		}},
	}

	if err := original.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := loaded.Sessions[0].Repos[0].PRNumber; got != "420" {
		t.Errorf("loaded PRNumber = %q, want %q", got, "420")
	}
}

func TestEnsureRepoPR_UpdatesPRNumberOnAnExistingRow(t *testing.T) {
	s := &State{}
	s.UpsertSession("pr-420", "PR #420", 1)
	s.EnsureRepo("pr-420", "/repo1", "/wt1") // how older builds wrote it: no PR number

	s.EnsureRepoPR("pr-420", "/repo1", "/wt1", "420")

	// Without the update the row stays PR-less forever, so the duplicate
	// guard never protects a session created before pr_number existed.
	want := []Repo{{RepoPath: "/repo1", WorktreePath: "/wt1", PRNumber: "420"}}
	if got := s.ReposFor("pr-420"); !reflect.DeepEqual(got, want) {
		t.Errorf("ReposFor = %+v, want %+v", got, want)
	}
}

func TestEnsureRepoPR_DoesNotChangeTheWorktreePathOfAnExistingRow(t *testing.T) {
	s := &State{}
	s.UpsertSession("pr-420", "PR #420", 1)
	s.EnsureRepo("pr-420", "/repo1", "/wt1")

	s.EnsureRepoPR("pr-420", "/repo1", "/wt1-different", "420")

	if got := s.ReposFor("pr-420")[0].WorktreePath; got != "/wt1" {
		t.Errorf("WorktreePath = %q, want %q (EnsureRepo's no-clobber rule still holds)", got, "/wt1")
	}
}
