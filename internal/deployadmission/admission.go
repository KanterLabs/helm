// Package deployadmission holds a process-lifetime lock after validating the
// fixed private-data authority. It does not authenticate a container image or
// establish that other writers have drained.
package deployadmission

import (
	"errors"
	"os"
	"sync"
)

const (
	protectedData = "/var/lib/roadmap/data"
	authorityDir  = "/etc/helm-admission"
	privateID     = 70541
)

// Lease retains the permanent lock descriptor until Close or process exit.
// Its descriptor is close-on-exec: a child must independently obtain admission.
type Lease struct {
	fd   *os.File
	once sync.Once
	err  error
}

// Close releases admission without changing the permanent lock file.
// Calling Close more than once is safe.
func (l *Lease) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if l.fd != nil {
			l.err = l.fd.Close()
		}
	})
	return l.err
}

func denied(reason string) error { return errors.New("runtime admission denied: " + reason) }
