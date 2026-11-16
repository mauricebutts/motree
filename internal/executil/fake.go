package executil

import "sync"

// Call represents a recorded command call for testing.
type Call struct {
	Name string
	Args []string
}

// Response represents a canned response for a queued command execution.
type Response struct {
	Stdout string
	Stderr string
	Err    error
}

// FakeRunner is a test double that records calls and returns canned responses.
//
// Run is safe for concurrent use (guarded by mu): some flows under test
// (e.g. mt ls's fzfutil reload poller, which runs in a background
// goroutine concurrently with the blocking fzf invocation by design) call
// Run from more than one goroutine at once, and without this guard
// concurrent appends to Calls (or increments of queueIndex) would race.
type FakeRunner struct {
	Calls []Call
	// Responses can be set to a function that returns results for each call
	Responses func(call Call) (stdout, stderr string, err error)
	// Queue for sequential responses (used if Responses is nil)
	Queue      []Response
	queueIndex int

	mu sync.Mutex
}

// Run records the call and returns either a queued response or the result from the Responses function.
func (f *FakeRunner) Run(name string, args ...string) (stdout, stderr string, err error) {
	call := Call{Name: name, Args: append([]string{}, args...)}

	f.mu.Lock()
	f.Calls = append(f.Calls, call)
	f.mu.Unlock()

	// Use Responses function if provided
	if f.Responses != nil {
		return f.Responses(call)
	}

	// Otherwise use queued responses
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.queueIndex < len(f.Queue) {
		resp := f.Queue[f.queueIndex]
		f.queueIndex++
		return resp.Stdout, resp.Stderr, resp.Err
	}

	return "", "", nil
}
