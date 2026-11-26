package nas

import (
	"crypto/hmac"
	"crypto/sha256"
	"strings"
)

// root's password (user 2026-11-23: "加一个随机吧"): a keelNAS made by keel has no empty root
// password any more. It is derived from the seed's key, so keel, which wrote the seed, computes
// the same one and shows it to an administrator on request (keelOS POST
// /v1/instances/{id}/root-password, audited); nothing else stores or sends it. A new seed (a
// new key) gives a new password. keelnasd sets it at every start; ssh stays without root login
// (sshd_config's default), so it is for the serial console.

const rootPwAlphabet = "23456789abcdefghjkmnpqrstuvwxyz" // no 0/o, 1/l/i

// RootPassword is the root password for a seed key: 16 characters in four groups of four.
// keelOS keel/internal/web/nasrootpw.go has the same function; their tests share a vector.
func RootPassword(key string) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte("keelnas root password"))
	sum := m.Sum(nil)
	var b strings.Builder
	for i := 0; i < 16; i++ {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(rootPwAlphabet[int(sum[i])%len(rootPwAlphabet)])
	}
	return b.String()
}

// ApplyRootPassword sets root's password from the seed (no seed: nothing changes).
func (s *Server) ApplyRootPassword() {
	s.hmu.Lock()
	sd := s.seed
	s.hmu.Unlock()
	if sd == nil || sd.Key == "" {
		return
	}
	if _, err := s.Sys.Run(RootPassword(sd.Key)+"\n", "pw", "usermod", "root", "-h", "0"); err != nil {
		s.Log.Printf("root password: %v", err)
		return
	}
	s.Log.Printf("root password set from the seed (keel shows it to an administrator)")
}
