package engine

import (
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
)

// sshProxy routes ssh connections through an sshDialer bound only by a
// deadline and a budget, with no Load context, for the dialer's own tests.
func sshProxy(budget *gitBudget, deadline time.Time) (transport.ProxyOptions, func()) {
	return routeSSH(&sshDialer{budget: budget, deadline: deadline})
}
