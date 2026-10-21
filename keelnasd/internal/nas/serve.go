package nas

import (
	"net"
	"net/http"
	"os"
	"time"
)

const Socket = "/var/run/keelnasd.sock"

// Serve applies the state once, adopts a seed disk if there is one, and then serves the API on the Unix socket (no key, root only)
// and, when the state has a listen address, on TCP with the key. It returns only on an error
// of the Unix socket.
func (s *Server) Serve(socket string) error {
	// keel's internal link first (network.go): keel can reach this keelNAS before the pools are
	// imported, the services run and whatever the LAN does (user 2026-10-03: "管理网应该早就起来了吧")
	if sd, _ := s.findSeed(); sd != nil {
		s.hmu.Lock()
		s.seed = sd
		s.hmu.Unlock()
		s.ApplyInternal()
	}
	if s.State().Fresh() {
		// A new system disk (or a new keelNAS) with the old data disks: take the settings back.
		pool, errs, err := s.Restore()
		switch {
		case err != nil:
			s.Log.Printf("restore: %v", err)
		case pool == "":
			s.Log.Printf("fresh settings and no pool with a copy of them: nothing to restore")
		default:
			s.Log.Printf("keelnasd %s: restored from %s and applied, %d errors", s.Version, pool, len(errs))
		}
	}
	if s.RestoredFrom() == "" {
		errs := s.Apply()
		s.Log.Printf("keelnasd %s: applied the state, %d errors", s.Version, len(errs))
	}
	go s.Schedule()

	// The seed disk (docs/04 section 10): its key and port, then the registration with keel.
	sd := s.UseSeed()
	h := s.Handler()
	st := s.State()
	if err := s.ApplyNetwork(); err != nil { // the LAN as state.json says (network.go)
		s.Log.Printf("network: %v", err)
	}
	if sd != nil {
		s.StartRegistration(sd)
	}
	if st.Listen != "" {
		go s.serveTCP(st.Listen, RequireKey(st.Key, h))
	}

	os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		ln.Close()
		return err
	}
	s.Log.Printf("listening on %s", socket)
	srv := &http.Server{Handler: s.LocalHandler(), ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(ln)
}

// serveTCP keeps trying: at boot the address may come from DHCP a little later.
func (s *Server) serveTCP(addr string, h http.Handler) {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	for i := 0; ; i++ {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			if i%12 == 0 {
				s.Log.Printf("listen %s: %v (trying again every 5 s)", addr, err)
			}
			time.Sleep(5 * time.Second)
			continue
		}
		s.Log.Printf("listening on %s (key required)", addr)
		err = srv.Serve(ln)
		s.Log.Printf("listen %s: %v", addr, err)
		time.Sleep(5 * time.Second)
	}
}
