package auth

import (
	"fmt"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// bcrypt is intentionally used instead of a home-grown password KDF. It is
// pure Go, has a well-reviewed implementation, and remains compatible with a
// static Linux build.
const bcryptCost = 12

var (
	dummyHashOnce sync.Once
	dummyHash     string
)

func HashPassword(password string) (string, error) {
	if utf8.RuneCountInString(password) < 12 {
		return "", fmt.Errorf("password must be at least 12 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	return string(hash), err
}

func CheckPassword(password, encoded string) bool {
	return bcrypt.CompareHashAndPassword([]byte(encoded), []byte(password)) == nil
}

// dummyPasswordHash keeps unknown-account failures on the same expensive
// bcrypt path as a known account. It is generated once and never persisted.
func dummyPasswordHash() string {
	dummyHashOnce.Do(func() {
		hash, err := bcrypt.GenerateFromPassword([]byte("helm-invalid-login-dummy"), bcryptCost)
		if err == nil {
			dummyHash = string(hash)
		}
	})
	return dummyHash
}
