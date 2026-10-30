// Package store persists servers and the profile in %ProgramData%\Kordo.
// Server parameters (keys, UUIDs, passwords) are encrypted with DPAPI.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/kordo-vpn/kordo/core/model"
)

// Protector encrypts secrets (DPAPI on Windows; identity in tests).
type Protector interface {
	Protect([]byte) ([]byte, error)
	Unprotect([]byte) ([]byte, error)
}

type persistedServer struct {
	model.Server
	Params    json.RawMessage `json:"params,omitempty"` // only for plaintext (tests)
	ParamsEnc []byte          `json:"paramsEnc,omitempty"`
}

type persisted struct {
	Version int               `json:"version"`
	Profile model.Profile     `json:"profile"`
	Servers []persistedServer `json:"servers"`
}

type Store struct {
	path string
	prot Protector
	mu   sync.RWMutex
	prof model.Profile
	srv  []model.Server
}

func Open(path string, prot Protector) (*Store, error) {
	s := &Store{path: path, prot: prot, prof: model.DefaultProfile()}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var p persisted
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	s.prof = p.Profile
	if s.prof.Rules == nil {
		s.prof.Rules = []model.AppRule{}
	}
	for _, ps := range p.Servers {
		srv := ps.Server
		switch {
		case len(ps.ParamsEnc) > 0:
			plain, err := prot.Unprotect(ps.ParamsEnc)
			if err != nil {
				return nil, fmt.Errorf("decrypt server %s: %w", srv.Name, err)
			}
			srv.Params = plain
		default:
			srv.Params = ps.Params
		}
		s.srv = append(s.srv, srv)
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	p := persisted{Version: 1, Profile: s.prof}
	for _, srv := range s.srv {
		enc, err := s.prot.Protect(srv.Params)
		if err != nil {
			return fmt.Errorf("encrypt server %s: %w", srv.Name, err)
		}
		ps := persistedServer{Server: srv, ParamsEnc: enc}
		ps.Server.Params = nil
		p.Servers = append(p.Servers, ps)
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(s.path, b)
}

// WriteFileAtomic writes via a temp file and rename (MoveFileEx REPLACE_EXISTING on Windows).
func WriteFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

func NewID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func clone[T any](v T) T {
	b, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(b, &out)
	return out
}

// Snapshot returns deep copies of the profile and servers.
func (s *Store) Snapshot() (model.Profile, []model.Server) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	srv := clone(s.srv)
	if srv == nil {
		srv = []model.Server{}
	}
	return clone(s.prof), srv
}

// Update applies fn to copies; the result is validated and saved atomically.
func (s *Store) Update(fn func(p *model.Profile, servers *[]model.Server) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, srv := clone(s.prof), clone(s.srv)
	if err := fn(&p, &srv); err != nil {
		return err
	}
	sort.SliceStable(p.Rules, func(i, j int) bool { return p.Rules[i].Priority < p.Rules[j].Priority })
	if err := p.Validate(srv); err != nil {
		return err
	}
	oldP, oldS := s.prof, s.srv
	s.prof, s.srv = p, srv
	if err := s.saveLocked(); err != nil {
		s.prof, s.srv = oldP, oldS
		return err
	}
	return nil
}

func (s *Store) Server(id string) (model.Server, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, srv := range s.srv {
		if srv.ID == id {
			return clone(srv), true
		}
	}
	return model.Server{}, false
}

// PlainProtector stores secrets unencrypted (tests only).
type PlainProtector struct{}

func (PlainProtector) Protect(b []byte) ([]byte, error)   { return b, nil }
func (PlainProtector) Unprotect(b []byte) ([]byte, error) { return b, nil }
