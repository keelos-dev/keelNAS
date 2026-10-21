package nas

import (
	"net/http"
)

// Handing the settings to a new system disk (keelOS docs/54 §4.7, docs/05 section 9): keelOS
// upgrades a keelNAS by replacing its system disk with a fresh clone of the new package. The
// settings live on the system disk (knsys/state); with a data pool the new keelnasd takes them
// back from the pool's copy (Restore), without one they would be lost. So keel asks the old
// keelnasd for them before it shuts the guest down (GET /v1/state/export) and gives them to the
// new one once it answers (POST /v1/state/import). The key and listen address are not part of
// the hand-over: the seed disk gives them again.

// Handover is what export gives and import takes: the state and the Samba passwords
// (pdbedit -L -w, the same text as a pool's .keelnas/smbpasswd).
type Handover struct {
	State     *State `json:"state"`
	Smbpasswd string `json:"smbpasswd"`
	Version   string `json:"version,omitempty"` // the keelnasd that wrote it
}

func (s *Server) getStateExport(r *http.Request) (any, error) {
	st := s.State() // a copy
	st.Key, st.Listen = "", ""
	s.mu.Lock()
	pw, err := s.Sys.Run("", "pdbedit", "-s", SmbConf, "-L", "-w")
	s.mu.Unlock()
	if err != nil && len(st.Users) > 0 {
		return nil, err
	}
	return Handover{State: st, Smbpasswd: pw, Version: s.Version}, nil
}

// Import takes settings handed over from the keelNAS this one replaces, if the state is fresh
// (a new system disk whose boot found no pool copy). The key and listen address stay this
// state's. It returns the errors of the apply.
func (s *Server) Import(h Handover) ([]string, error) {
	if h.State == nil {
		return nil, bad("state missing")
	}
	s.mu.Lock()
	if !s.st.Fresh() {
		s.mu.Unlock()
		return nil, errNotFresh
	}
	st := h.State.Clone()
	st.normalize()
	st.Key, st.Listen = s.st.Key, s.st.Listen
	if err := SaveState(s.Sys, s.StatePath, st); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.st = st
	if h.Smbpasswd != "" {
		s.pendingPasswd = []byte(h.Smbpasswd)
	}
	s.hmu.Lock()
	s.restoredFrom = "handover"
	s.hmu.Unlock()
	s.Log.Printf("took the settings handed over from keelnasd %s", h.Version)
	errs := s.applyLocked()
	s.mu.Unlock()
	if err := s.ApplyNetwork(); err != nil { // the LAN as the handed-over state says
		errs = append(errs, "network: "+err.Error())
	}
	return errs, nil
}

func (s *Server) postStateImport(r *http.Request) (any, error) {
	var h Handover
	if err := decode(r, &h); err != nil {
		return nil, err
	}
	errs, err := s.Import(h)
	if err != nil {
		return nil, err
	}
	if errs == nil {
		errs = []string{}
	}
	return map[string]any{"ok": len(errs) == 0, "errors": errs}, nil
}
