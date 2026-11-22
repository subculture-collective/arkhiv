package sourcecatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"git.subcult.tv/subculture-collective/vhness/internal/preservation"
)

// ErrLocalAudioPending means a selected original, or its independent copy, is
// missing or differs. The source stays unapproved and the batch retries it.
var ErrLocalAudioPending = errors.New("local original or independent copy not ready")

const maxRecoveryReceipt = 64 << 20

// RecoveryReceipt describes the independent copy of a local_preserve
// collection's originals on another host. Its exact bytes' SHA-256 is recorded
// as each review's independent_recovery_sha256. Recording it proves only that
// the receipt listed matching checksums, not that a restore was performed.
type RecoveryReceipt struct {
	Collection string          `json:"collection"`
	Host       string          `json:"host"`
	Location   string          `json:"location"`
	VerifiedAt time.Time       `json:"verified_at"`
	Files      []RecoveredFile `json:"files"`
	digest     string
	index      map[string]RecoveredFile
}

// RecoveredFile is one file of the independent copy. Path is relative to the
// collection directory: <source identifier>/<source file name>.
type RecoveredFile struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// ParseRecoveryReceipt validates a receipt for collection.
func ParseRecoveryReceipt(raw []byte, collection string) (*RecoveryReceipt, error) {
	if len(raw) > maxRecoveryReceipt {
		return nil, fmt.Errorf("recovery receipt exceeds %d bytes", maxRecoveryReceipt)
	}
	var r RecoveryReceipt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&r); err != nil {
		return nil, fmt.Errorf("invalid recovery receipt: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("recovery receipt must be one JSON object")
	}
	if r.Collection != collection || strings.TrimSpace(r.Host) == "" || strings.TrimSpace(r.Location) == "" || r.VerifiedAt.IsZero() || len(r.Files) == 0 {
		return nil, fmt.Errorf("recovery receipt needs this collection, the independent host and location, verified_at and files")
	}
	r.index = make(map[string]RecoveredFile, len(r.Files))
	for _, f := range r.Files {
		if !filepath.IsLocal(f.Path) || filepath.Clean(f.Path) != f.Path || strings.ContainsAny(f.Path, "\\\x00") || f.Bytes <= 0 || !lowerHex(f.SHA256, 64) {
			return nil, fmt.Errorf("invalid recovery receipt file %q", f.Path)
		}
		if _, seen := r.index[f.Path]; seen {
			return nil, fmt.Errorf("duplicate recovery receipt file %q", f.Path)
		}
		r.index[f.Path] = f
	}
	r.digest = domain.Digest(raw)
	return &r, nil
}

// SHA256 is the digest of the receipt's exact bytes.
func (r *RecoveryReceipt) SHA256() string { return r.digest }

// localManifest checks every selected file of source item id under
// <media>/<collection>/<id>/<file> against the size and MD5 in the retained
// source snapshot and against the independent copy, then returns the
// preservation manifest. It checks presence and sizes before hashing anything,
// and returns an error rather than a partial manifest.
func (s *Store) localManifest(ctx context.Context, c domain.Collection, id string, files []map[string]any) ([]domain.LocalOriginal, error) {
	if !filepath.IsAbs(s.Media) {
		return nil, fmt.Errorf("local source audio review requires an absolute VHNESS_MEDIA")
	}
	if s.Recovery == nil || s.Recovery.Collection != c.ID {
		return nil, fmt.Errorf("local source audio review requires this collection's independent recovery receipt")
	}
	dir := filepath.Join(s.Media, c.ID)
	used, err := preservation.Usage(dir)
	if err != nil {
		return nil, err
	}
	if used > c.Budget {
		return nil, fmt.Errorf("preservation byte budget exhausted: %d bytes under %s exceed %d", used, dir, c.Budget)
	}
	free, err := preservation.FreeBytes(dir)
	if err != nil {
		return nil, err
	}
	if free < c.Reserve {
		return nil, fmt.Errorf("free-space reserve would be violated: %d bytes free, %d reserved", free, c.Reserve)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	type selected struct {
		name, path, md5 string
		bytes           int64
		mirror          RecoveredFile
	}
	checks := make([]selected, 0, len(files))
	var total int64
	for _, file := range files {
		name := sourceFilename(file["name"])
		if !domain.SafeFile(name) || name == ".." {
			return nil, fmt.Errorf("%w: %q is not a root file name", ErrUnsupportedAudio, name)
		}
		size, sizeOK := sourceSize(file["size"])
		sum := strings.ToLower(Text(file["md5"]))
		if !sizeOK || !lowerHex(sum, 32) {
			return nil, fmt.Errorf("%w: %q has no source size and MD5", ErrUnsupportedAudio, name)
		}
		rel := id + "/" + name
		info, err := root.Lstat(rel)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%w: %s is missing or not a regular file", ErrLocalAudioPending, rel)
		}
		if info.Size() != size {
			return nil, fmt.Errorf("%w: %s has %d bytes, the source lists %d", ErrLocalAudioPending, rel, info.Size(), size)
		}
		mirror, ok := s.Recovery.index[rel]
		if !ok || mirror.Bytes != size {
			return nil, fmt.Errorf("%w: the independent copy lacks %s", ErrLocalAudioPending, rel)
		}
		if size > c.Budget-total {
			return nil, fmt.Errorf("manifest exceeds the collection byte budget")
		}
		total += size
		checks = append(checks, selected{name: name, path: rel, md5: sum, bytes: size, mirror: mirror})
	}
	manifest := make([]domain.LocalOriginal, 0, len(checks))
	for _, check := range checks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, err := root.Open(check.path)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrLocalAudioPending, check.path, err)
		}
		sha, md5sum, n, err := preservation.Hash(f)
		f.Close()
		if err != nil {
			return nil, err
		}
		if n != check.bytes || md5sum != check.md5 {
			return nil, fmt.Errorf("%w: %s differs from the source MD5", ErrLocalAudioPending, check.path)
		}
		if sha != check.mirror.SHA256 {
			return nil, fmt.Errorf("%w: the independent copy of %s differs", ErrLocalAudioPending, check.path)
		}
		manifest = append(manifest, domain.LocalOriginal{File: check.name, Bytes: check.bytes, SHA256: sha, Path: filepath.Join(c.ID, check.path)})
	}
	return manifest, nil
}

// sourceSize reads Internet Archive's file size, a decimal string or number.
func sourceSize(value any) (int64, bool) {
	switch v := value.(type) {
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil && n > 0
	case float64:
		return int64(v), v > 0 && v == math.Trunc(v) && v < 1<<53
	}
	return 0, false
}

func lowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
