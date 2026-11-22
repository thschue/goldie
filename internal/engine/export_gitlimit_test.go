package engine

// SetGitLoadLimits lowers one Load's budget for s, so a test of the limits
// does not move hundreds of megabytes.
func SetGitLoadLimits(s *GitSource, wire, mem int64) { s.maxWire, s.maxMem = wire, mem }
