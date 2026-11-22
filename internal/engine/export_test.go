package engine

import "time"

// GitLoadTimeout is the bound on one Load of s.
func GitLoadTimeout(s *GitSource) time.Duration { return s.timeout }

// SetGitLoadTimeout shortens the bound on one Load of s, so a test of the
// timeout does not wait two minutes.
func SetGitLoadTimeout(s *GitSource, d time.Duration) { s.timeout = d }

// SetGitLoadLimits lowers one Load's budget for s, so a test of the limits
// does not move hundreds of megabytes.
func SetGitLoadLimits(s *GitSource, wire, mem int64) { s.maxWire, s.maxMem = wire, mem }
