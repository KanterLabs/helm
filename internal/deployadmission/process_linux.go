//go:build linux

package deployadmission

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

type processCredentials struct {
	uid, euid, gid, egid int
	groups               []int
	status               string
}

func currentCredentials() (processCredentials, error) {
	p := processCredentials{uid: os.Getuid(), euid: os.Geteuid(), gid: os.Getgid(), egid: os.Getegid()}
	groups, err := os.Getgroups()
	if err != nil {
		return p, err
	}
	p.groups = groups
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return p, err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return p, err
	}
	if len(body) > 65536 {
		return p, denied("process status")
	}
	p.status = string(body)
	return p, nil
}

func (p processCredentials) valid() bool {
	if p.uid != privateID || p.euid != privateID || p.gid != privateID || p.egid != privateID || len(p.groups) != 1 || p.groups[0] != privateID {
		return false
	}
	seen := make(map[string]bool, 3)
	for _, line := range strings.Split(p.status, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if key != "CapPrm" && key != "CapEff" && key != "NoNewPrivs" {
			continue
		}
		if seen[key] {
			return false
		}
		seen[key] = true
		value = strings.TrimSpace(value)
		if key == "NoNewPrivs" {
			if value != "1" {
				return false
			}
			continue
		}
		if len(value) != 16 {
			return false
		}
		n, err := strconv.ParseUint(value, 16, 64)
		if err != nil || n != 0 {
			return false
		}
	}
	return len(seen) == 3
}

func runningExecutableHash() (string, error) {
	// /proc/self/exe is the kernel's reference to the running image, including
	// after its original pathname has been replaced. Follow this proc symlink.
	fd, err := unix.Open("/proc/self/exe", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(fd), "executable")
	defer f.Close()
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG {
		return "", denied("executable unavailable")
	}
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
