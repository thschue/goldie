package project

// Cause, OpenFlags and NoFollow share this package's file-opening helpers with
// other writers of repository files (the managed-block editor in internal/cli),
// so there is one copy of each.

// Cause drops the path an *fs.PathError or *os.LinkError carries and keeps the
// reason, such as "permission denied".
func Cause(err error) error { return cause(err) }

// OpenFlags keeps opening a FIFO or a terminal from blocking.
const OpenFlags = openFlags

// NoFollow makes an open fail on a symlink where the platform has O_NOFOLLOW.
const NoFollow = noFollow
