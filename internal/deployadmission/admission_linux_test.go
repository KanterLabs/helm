//go:build linux

package deployadmission

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func fixture(t *testing.T) (runtimeChecks, marker) {
	t.Helper()
	root := t.TempDir()
	c := runtimeChecks{dataPath: filepath.Join(root, "data"), authorityPath: filepath.Join(root, "authority")}
	for path, mode := range map[string]os.FileMode{c.dataPath: 0700, c.authorityPath: 0750} {
		if err := os.Mkdir(path, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	for path, mode := range map[string]os.FileMode{filepath.Join(c.dataPath, "roadmap.db"): 0600, filepath.Join(c.authorityPath, "runtime.lock"): 0640} {
		if err := os.WriteFile(path, nil, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	c.credentials = func() (processCredentials, error) {
		return processCredentials{uid: 70541, euid: 70541, gid: 70541, egid: 70541, groups: []int{70541}, status: "CapPrm:\t0000000000000000\nCapEff:\t0000000000000000\nNoNewPrivs:\t1\n"}, nil
	}
	c.stat = func(fd int) (unix.Stat_t, error) {
		var s unix.Stat_t
		err := unix.Fstat(fd, &s)
		if s.Mode&0777 == 0700 || s.Mode&0777 == 0600 {
			s.Uid = 70541
		} else {
			s.Uid = 0
		}
		s.Gid = 70541
		return s, err
	}
	c.readOnly = func(int) (bool, error) { return true, nil }
	c.executableHash = func() (string, error) { return strings.Repeat("b", 64), nil }
	m := validMarker()
	var ds, ls unix.Stat_t
	if err := unix.Stat(c.dataPath, &ds); err != nil {
		t.Fatal(err)
	}
	if err := unix.Stat(filepath.Join(c.authorityPath, "runtime.lock"), &ls); err != nil {
		t.Fatal(err)
	}
	m.DataDevice = uint64(ds.Dev)
	m.DataInode = ds.Ino
	m.LockDevice = uint64(ls.Dev)
	m.LockInode = ls.Ino
	writeFixtureMarker(t, c, m)
	return c, m
}

func writeFixtureMarker(t *testing.T, c runtimeChecks, m marker) {
	t.Helper()
	path := filepath.Join(c.authorityPath, "marker.json")
	if err := os.WriteFile(path, markerJSON(t, m), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
}

func TestLeasePermanentLock(t *testing.T) {
	c, _ := fixture(t)
	path := filepath.Join(c.authorityPath, "runtime.lock")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := acquire(c)
	if err != nil {
		t.Fatal(err)
	}
	if l == nil {
		t.Fatal("missing lease")
	}
	flags, err := unix.FcntlInt(l.fd.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("lease descriptor not close-on-exec")
	}
	if second, err := acquire(c); err == nil || second != nil {
		t.Fatal("admitted second lease")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l, err = acquire(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("permanent lock replaced")
	}
}

func TestProtectedFailures(t *testing.T) {
	cases := map[string]func(*testing.T, *runtimeChecks, *marker){
		"missing authority": func(t *testing.T, c *runtimeChecks, m *marker) { os.RemoveAll(c.authorityPath) },
		"missing marker": func(t *testing.T, c *runtimeChecks, m *marker) {
			os.Remove(filepath.Join(c.authorityPath, "marker.json"))
		},
		"missing lock": func(t *testing.T, c *runtimeChecks, m *marker) {
			os.Remove(filepath.Join(c.authorityPath, "runtime.lock"))
		},
		"missing database": func(t *testing.T, c *runtimeChecks, m *marker) { os.Remove(filepath.Join(c.dataPath, "roadmap.db")) },
		"corrupt marker": func(t *testing.T, c *runtimeChecks, m *marker) {
			os.WriteFile(filepath.Join(c.authorityPath, "marker.json"), []byte("{"), 0640)
		},
		"data identity": func(t *testing.T, c *runtimeChecks, m *marker) { m.DataInode++; writeFixtureMarker(t, *c, *m) },
		"lock identity": func(t *testing.T, c *runtimeChecks, m *marker) { m.LockInode++; writeFixtureMarker(t, *c, *m) },
		"hash mismatch": func(t *testing.T, c *runtimeChecks, m *marker) {
			c.executableHash = func() (string, error) { return strings.Repeat("e", 64), nil }
		},
		"hash unavailable": func(t *testing.T, c *runtimeChecks, m *marker) {
			c.executableHash = func() (string, error) { return "", errors.New("secret path") }
		},
		"authority writable": func(t *testing.T, c *runtimeChecks, m *marker) {
			c.readOnly = func(int) (bool, error) { return false, nil }
		},
		"statfs failure": func(t *testing.T, c *runtimeChecks, m *marker) {
			c.readOnly = func(int) (bool, error) { return false, errors.New("secret path") }
		},
		"metadata failure": func(t *testing.T, c *runtimeChecks, m *marker) {
			c.stat = func(int) (unix.Stat_t, error) { return unix.Stat_t{}, errors.New("secret path") }
		},
		"owner": func(t *testing.T, c *runtimeChecks, m *marker) {
			base := c.stat
			c.stat = func(fd int) (unix.Stat_t, error) { s, e := base(fd); s.Uid = 1000; return s, e }
		},
		"group": func(t *testing.T, c *runtimeChecks, m *marker) {
			base := c.stat
			c.stat = func(fd int) (unix.Stat_t, error) { s, e := base(fd); s.Gid = 1000; return s, e }
		},
		"lock replaced": func(t *testing.T, c *runtimeChecks, m *marker) {
			c.afterLock = func() {
				os.Rename(filepath.Join(c.authorityPath, "runtime.lock"), filepath.Join(c.authorityPath, "old.lock"))
				os.WriteFile(filepath.Join(c.authorityPath, "runtime.lock"), nil, 0640)
			}
		},
		"fresh marker": func(t *testing.T, c *runtimeChecks, m *marker) {
			c.afterLock = func() { m.Phase = "closed"; writeFixtureMarker(t, *c, *m) }
		},
	}
	for _, file := range []string{"data", "authority", "roadmap.db", "marker.json", "runtime.lock"} {
		file := file
		cases[file+" mode"] = func(t *testing.T, c *runtimeChecks, m *marker) {
			if err := os.Chmod(fixturePath(*c, file), 0770); err != nil {
				t.Fatal(err)
			}
		}
		cases[file+" special mode"] = func(t *testing.T, c *runtimeChecks, m *marker) {
			if err := unix.Chmod(fixturePath(*c, file), 04700); err != nil {
				t.Fatal(err)
			}
		}
		cases[file+" symlink"] = func(t *testing.T, c *runtimeChecks, m *marker) {
			path := fixturePath(*c, file)
			if err := os.Rename(path, path+".real"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(path+".real", path); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c, m := fixture(t)
			mutate(t, &c, &m)
			l, err := acquire(c)
			if l != nil {
				l.Close()
			}
			if err == nil {
				t.Fatal("accepted invalid protected state")
			}
			if len(err.Error()) > 160 || strings.Contains(err.Error(), c.dataPath) || strings.Contains(err.Error(), "secret") {
				t.Fatal("unbounded or sensitive error")
			}
		})
	}
}

func fixturePath(c runtimeChecks, file string) string {
	switch file {
	case "data":
		return c.dataPath
	case "authority":
		return c.authorityPath
	case "roadmap.db":
		return filepath.Join(c.dataPath, file)
	default:
		return filepath.Join(c.authorityPath, file)
	}
}

func TestPublicAbsentOnly(t *testing.T) {
	c, _ := fixture(t)
	os.RemoveAll(c.dataPath)
	c.credentials = func() (processCredentials, error) {
		t.Fatal("public checked credentials")
		return processCredentials{}, nil
	}
	l, err := acquire(c)
	if err != nil || l != nil {
		t.Fatal("public absent path rejected")
	}
}

func TestProcessCredentialFailures(t *testing.T) {
	cases := map[string]func(*processCredentials){
		"old uid": func(p *processCredentials) { p.uid = 1000 }, "effective uid": func(p *processCredentials) { p.euid = 1000 }, "gid": func(p *processCredentials) { p.gid = 1000 }, "effective gid": func(p *processCredentials) { p.egid = 1000 },
		"extra groups": func(p *processCredentials) { p.groups = []int{70541, 0} }, "missing group": func(p *processCredentials) { p.groups = nil },
		"effective capabilities": func(p *processCredentials) {
			p.status = strings.Replace(p.status, "CapEff:\t0000000000000000", "CapEff:\t0000000000000001", 1)
		},
		"permitted capabilities": func(p *processCredentials) {
			p.status = strings.Replace(p.status, "CapPrm:\t0000000000000000", "CapPrm:\t0000000000000001", 1)
		},
		"incomplete capabilities": func(p *processCredentials) { p.status = "CapEff:\t0000000000000000\nNoNewPrivs:\t1\n" },
		"invalid capabilities": func(p *processCredentials) {
			p.status = strings.Replace(p.status, "CapEff:\t0000000000000000", "CapEff:\tgarbage", 1)
		},
		"privileges": func(p *processCredentials) {
			p.status = strings.Replace(p.status, "NoNewPrivs:\t1", "NoNewPrivs:\t0", 1)
		},
		"duplicate status": func(p *processCredentials) { p.status += "NoNewPrivs:\t1\n" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c, _ := fixture(t)
			p, _ := c.credentials()
			mutate(&p)
			c.credentials = func() (processCredentials, error) { return p, nil }
			if l, err := acquire(c); err == nil {
				if l != nil {
					l.Close()
				}
				t.Fatal("accepted invalid credentials")
			}
		})
	}
	t.Run("unavailable", func(t *testing.T) {
		c, _ := fixture(t)
		c.credentials = func() (processCredentials, error) { return processCredentials{}, errors.New("private") }
		if l, err := acquire(c); err == nil {
			if l != nil {
				l.Close()
			}
			t.Fatal("accepted unavailable credentials")
		}
	})
}
