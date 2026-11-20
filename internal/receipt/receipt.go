// Package receipt stores the receipts of kipple clean --apply runs (design
// D8): one JSON file per run in <state>/receipts, where <state> is
// $KIPPLE_STATE_DIR, defaulting to ~/Library/Application Support/kipple
// (cmd/ resolves it; this package never reads the real home).
//
// A receipt is written before the first move and rewritten after every item
// (Store.Save replaces it atomically), so an interrupted run still records
// what it moved. Receipts are read back as untrusted input: Load and List
// decode them strictly, and restore re-checks every path they name.
package receipt

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rbutera/kipple/internal/plan"
)

// DirName is the receipts folder inside the state directory.
const DirName = "receipts"

// idPattern matches receipt IDs: a UTC timestamp in ISO 8601 basic format
// (no colons, so the ID is a portable file name) and six random characters.
var idPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[a-z0-9]{6}$`)

// ValidID reports whether id has the form of a receipt ID.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// NewID returns a receipt ID for a run that started at now.
func NewID(now time.Time) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on supported platforms; fall back to the
		// clock so an ID is still produced.
		n := now.UnixNano()
		for i := range b {
			b[i] = byte(n >> (8 * i))
		}
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return now.UTC().Format("20060102T150405Z") + "-" + string(b[:])
}

// Store is the receipts folder of one state directory.
type Store struct {
	dir string
}

// Open returns the store for stateDir, which must be absolute and clean.
// Nothing is created until the first Create.
func Open(stateDir string) (Store, error) {
	if !filepath.IsAbs(stateDir) || filepath.Clean(stateDir) != stateDir {
		return Store{}, fmt.Errorf("receipt: state directory %q is not an absolute clean path", stateDir)
	}
	return Store{dir: filepath.Join(stateDir, DirName)}, nil
}

// Dir returns the receipts folder.
func (s Store) Dir() string { return s.dir }

// PathOf returns where the receipt with id is stored.
func (s Store) PathOf(id string) string { return filepath.Join(s.dir, id+".json") }

// Entry is a receipt and the file it was read from.
type Entry struct {
	Path    string
	Receipt plan.Receipt
}

// Create gives r a fresh ID (from r.CreatedAt) and writes it, never
// replacing an existing receipt. It returns the receipt's path.
func (s Store) Create(r *plan.Receipt) (string, error) {
	if err := s.ensureDir(); err != nil {
		return "", err
	}
	for i := 0; i < 8; i++ {
		r.ID = NewID(r.CreatedAt)
		err := s.write(r, false)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		return s.PathOf(r.ID), nil
	}
	return "", errors.New("receipt: could not choose an unused receipt ID")
}

// Save atomically replaces the stored receipt r.ID with r.
func (s Store) Save(r plan.Receipt) error {
	if !ValidID(r.ID) {
		return fmt.Errorf("receipt: invalid ID %q", r.ID)
	}
	return s.write(&r, true)
}

func encode(r *plan.Receipt) ([]byte, error) {
	r.SchemaVersion = plan.SchemaVersion
	r.Normalize()
	var buf bytes.Buffer
	if err := plan.Encode(&buf, r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// List returns every readable receipt, newest first. Files that are not
// valid receipts are reported in the error slice and left alone.
func (s Store) List() ([]Entry, []error) {
	names, err := s.names()
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{fmt.Errorf("receipt: %s: %w", s.dir, err)}
	}
	var out []Entry
	var errs []error
	for _, n := range names {
		if !strings.HasSuffix(n, ".json") || !ValidID(strings.TrimSuffix(n, ".json")) {
			continue
		}
		r, err := s.read(n)
		if err != nil {
			errs = append(errs, fmt.Errorf("receipt: %s: %w", filepath.Join(s.dir, n), err))
			continue
		}
		if r.ID+".json" != n {
			errs = append(errs, fmt.Errorf("receipt: %s: its id %q does not match the file name", filepath.Join(s.dir, n), r.ID))
			continue
		}
		out = append(out, Entry{Path: filepath.Join(s.dir, n), Receipt: r})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Receipt, out[j].Receipt
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.After(b.CreatedAt)
		}
		return a.ID > b.ID
	})
	return out, errs
}

// Load returns the receipt with the given ID from the store.
func (s Store) Load(id string) (Entry, error) {
	if !ValidID(id) {
		return Entry{}, fmt.Errorf("receipt: %q is not a receipt ID", id)
	}
	r, err := s.read(id + ".json")
	if err != nil {
		return Entry{}, fmt.Errorf("receipt %s: %w", id, err)
	}
	if r.ID != id {
		return Entry{}, fmt.Errorf("receipt %s: its id %q does not match the file name", id, r.ID)
	}
	return Entry{Path: s.PathOf(id), Receipt: r}, nil
}

// LoadFile reads a receipt from any path (for kipple restore <file>). The
// file must be a regular file; a symlink is refused.
func LoadFile(path string) (Entry, error) {
	data, err := readFile(path)
	if err != nil {
		return Entry{}, fmt.Errorf("receipt: %w", err)
	}
	r, err := plan.DecodeReceipt(bytes.NewReader(data))
	if err != nil {
		return Entry{}, fmt.Errorf("%s: %w", path, err)
	}
	return Entry{Path: path, Receipt: r}, nil
}

// MarkRestored sets RestoredAt on the moved item with the given ID and
// reports whether it was found. The caller saves the receipt.
func MarkRestored(r *plan.Receipt, id string, at time.Time) bool {
	for i := range r.Moved {
		if r.Moved[i].ID == id {
			t := at.UTC()
			r.Moved[i].RestoredAt = &t
			return true
		}
	}
	return false
}
