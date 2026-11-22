package engine

import "time"

// NewGitSourceWithCABundle is NewGitSource with an extra trusted CA, so tests
// can serve HTTPS from httptest without turning TLS verification off.
func NewGitSourceWithCABundle(opts GitOptions, caPEM []byte) (*GitSource, error) {
	s, err := NewGitSource(opts)
	if err != nil {
		return nil, err
	}
	s.caBundle = caPEM
	return s, nil
}

// GitLoadTimeout is the bound on one Load of s.
func GitLoadTimeout(s *GitSource) time.Duration { return s.timeout }

// SetGitLoadTimeout shortens the bound on one Load of s, so a test of the
// timeout does not wait two minutes.
func SetGitLoadTimeout(s *GitSource, d time.Duration) { s.timeout = d }
