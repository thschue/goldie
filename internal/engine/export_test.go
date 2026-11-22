package engine

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
