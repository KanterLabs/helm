//go:build !linux

package deployadmission

import "os"

// Acquire permits public startup only when the protected path is absent.
// Protected admission requires Linux credential, mount and locking guarantees.
func Acquire() (*Lease, error) {
	if _, err := os.Lstat(protectedData); os.IsNotExist(err) {
		return nil, nil
	}
	return nil, denied("Linux required for protected data")
}
