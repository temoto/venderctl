package tele

import (
	"encoding/csv"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/juju/errors"
	"gopkg.in/hlandau/passlib.v1"
)

// username:password pairs, cached (stale) reading from file
type Secrets struct {
	sync.Mutex
	m     map[string]string
	valid time.Time
}

func (s *Secrets) CachedReadFile(path string, refreshInterval time.Duration) error {
	s.Lock()
	defer s.Unlock()
	if time.Since(s.valid) < refreshInterval {
		return nil
	}
	s.valid = time.Now()
	return s.UnsafeReadFile(path)
}

// Thread-unsafe. Use embedded Lock.
func (s *Secrets) UnsafeReadFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return errors.Annotatef(err, "Secrets.ReadFile: open path=%s", path)
	}
	defer f.Close()
	err = s.UnsafeReadFrom(f)
	return errors.Annotate(err, "Secrets.ReadFile")
}

// Thread-unsafe. Use embedded Lock.
func (s *Secrets) UnsafeReadFrom(r io.Reader) error {
	cr := csv.NewReader(r)
	cr.Comma = ':'
	cr.Comment = '#'
	cr.ReuseRecord = true
	cr.TrimLeadingSpace = true
	rows, err := cr.ReadAll()
	if err != nil {
		return err
	}
	m := make(map[string]string)
	for i, row := range rows {
		if numColumns := len(row); numColumns == 0 {
		} else if numColumns == 2 {
			username := row[0]
			hash := row[1]
			m[username] = hash
		} else {
			return errors.Errorf("Secrets.read invalid row %d username=%s", i, row[0])
		}
	}
	s.m = m
	return nil
}

func (s *Secrets) WriteFile(filepath string) error {
	s.Lock()
	defer s.Unlock()
	tempPath := filepath + ".tmp"
	temp, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|os.O_EXCL, 0600)
	if err != nil {
		return errors.Annotatef(err, "Secrets.WriteFile: open temp=%s", tempPath)
	}
	defer temp.Close()
	defer os.Remove(tempPath)
	if err = s.UnsafeWriteTo(temp); err != nil {
		return errors.Annotate(err, "Secrets.WriteFile")
	}
	if err = temp.Sync(); err != nil {
		return errors.Annotate(err, "Secrets.WriteFile: sync")
	}
	if err = os.Rename(tempPath, filepath); err != nil {
		return errors.Annotate(err, "Secrets.WriteFile: rename")
	}
	return nil
}

func (s *Secrets) UnsafeWriteTo(w io.Writer) error {
	cw := csv.NewWriter(w)
	cw.Comma = ':'
	order := make([]string, 0, len(s.m))
	for username := range s.m {
		order = append(order, username)
	}
	sort.Strings(order)
	for _, username := range order {
		hash := s.m[username]
		if err := cw.Write([]string{username, hash}); err != nil {
			return errors.Annotate(err, "Secrets.write")
		}
	}
	cw.Flush()
	return errors.Annotate(cw.Error(), "Secrets.write")
}

// Thread-unsafe. Use embedded Lock.
func (s *Secrets) UnsafeSet(username, secret string) error {
	hash, err := passlib.Hash(secret)
	if err != nil {
		return errors.Annotate(err, "passlib.Hash")
	}
	s.m[username] = hash
	return nil
}

func (s *Secrets) Verify(username, secret string) error {
	s.Lock()
	defer s.Unlock()
	hash, found := s.m[username]
	if !found {
		return errors.UserNotFoundf("")
	}
	newHash, err := passlib.Verify(secret, hash)
	if err != nil {
		return errors.NotValidf("")
	}
	if newHash != "" {
		// self.log.Debugf("updating secret for user=\"%s\"", pkt.Username)
		s.m[username] = newHash
	}
	return nil
}
