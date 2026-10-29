// Package ioslogs transfers fixed, bounded log snapshots without a shared container.
package ioslogs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const MaxFileSize = 2 << 20
const ChunkSize = 64 << 10

var Names = []string{"client.log", "client.log.1", "client.log.2", "privileged.log", "privileged.log.1", "privileged.log.2"}

func Allowed(name string) bool {
	for _, n := range Names {
		if n == name {
			return true
		}
	}
	return false
}

type Request struct {
	Action string `json:"action"`
	Token  string `json:"token,omitempty"`
	Name   string `json:"name,omitempty"`
	Offset int64  `json:"offset,omitempty"`
}
type File struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}
type Reply struct {
	Available bool   `json:"available"`
	Token     string `json:"token,omitempty"`
	Files     []File `json:"files,omitempty"`
	Data      []byte `json:"data,omitempty"`
}

type Store struct {
	mu               sync.Mutex
	directory, token string
	opened           map[string]*os.File
	sizes            map[string]int64
	expires          time.Time
	closed           bool
}

func NewStore(directory string) *Store { return &Store{directory: directory} }
func (s *Store) closeLocked() {
	for _, f := range s.opened {
		_ = f.Close()
	}
	s.opened = nil
	s.sizes = nil
	s.token = ""
}
func (s *Store) Close() { s.mu.Lock(); defer s.mu.Unlock(); s.closed = true; s.closeLocked() }

// Open descriptors pin rotated files, and reads stop at the size captured at
// creation. Appending and rename-based rotation cannot mix snapshot generations.
func (s *Store) Handle(r Request) (Reply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Reply{}, errors.New("iOS log store stopped")
	}
	if r.Action == "create" {
		s.closeLocked()
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return Reply{}, err
		}
		s.token = hex.EncodeToString(id)
		s.expires = time.Now().Add(5 * time.Minute)
		s.opened = map[string]*os.File{}
		s.sizes = map[string]int64{}
		result := Reply{Available: true, Token: s.token}
		for _, name := range Names {
			path := filepath.Join(s.directory, name)
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				s.closeLocked()
				return Reply{}, err
			}
			if !info.Mode().IsRegular() || info.Size() > MaxFileSize {
				s.closeLocked()
				return Reply{}, errors.New("invalid iOS log file")
			}
			f, err := os.Open(path)
			if err != nil {
				s.closeLocked()
				return Reply{}, err
			}
			info, err = f.Stat()
			if err != nil || !info.Mode().IsRegular() || info.Size() > MaxFileSize {
				f.Close()
				s.closeLocked()
				return Reply{}, errors.New("invalid iOS log file")
			}
			s.opened[name] = f
			s.sizes[name] = info.Size()
			result.Files = append(result.Files, File{Name: name, Size: info.Size(), Modified: info.ModTime().UnixNano()})
		}
		return result, nil
	}
	if s.token == "" || r.Token != s.token || time.Now().After(s.expires) {
		return Reply{}, errors.New("iOS log snapshot expired")
	}
	if r.Action == "close" {
		s.closeLocked()
		return Reply{Available: true}, nil
	}
	f := s.opened[r.Name]
	if r.Action != "read" || !Allowed(r.Name) || f == nil || r.Offset < 0 || r.Offset > s.sizes[r.Name] {
		return Reply{}, errors.New("invalid iOS log snapshot request")
	}
	data := make([]byte, min(int64(ChunkSize), s.sizes[r.Name]-r.Offset))
	n, err := f.ReadAt(data, r.Offset)
	if err != nil && err != io.EOF {
		return Reply{}, err
	}
	if n != len(data) {
		return Reply{}, io.ErrUnexpectedEOF
	}
	return Reply{Available: true, Data: data}, nil
}
