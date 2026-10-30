// Package backup creates, lists, prunes and restores compressed snapshots of
// the database.
package backup

import (
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

// Snapshotter writes a consistent copy of the live database.
type Snapshotter interface {
	Backup(ctx context.Context, dest string) error
}

// Info describes one backup file.
type Info struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Created int64  `json:"created"`
}

var nameRe = regexp.MustCompile(`^xrayscope-\d{8}-\d{6}\.db\.gz$`)

// ValidName reports whether name is a backup file name created by this
// package (guards against path traversal).
func ValidName(name string) bool { return nameRe.MatchString(name) }

// Create writes a new compressed snapshot into dir and removes the oldest
// ones beyond keep.
func Create(ctx context.Context, s Snapshotter, dir string, keep int) (Info, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Info{}, err
	}
	stamp := time.Now().Format("20060102-150405")
	tmp := filepath.Join(dir, "tmp-"+stamp+".db")
	defer os.Remove(tmp)
	if err := s.Backup(ctx, tmp); err != nil {
		return Info{}, fmt.Errorf("snapshot: %w", err)
	}
	final := filepath.Join(dir, "xrayscope-"+stamp+".db.gz")
	if err := gzipFile(tmp, final+".part"); err != nil {
		os.Remove(final + ".part")
		return Info{}, err
	}
	if err := os.Rename(final+".part", final); err != nil {
		return Info{}, err
	}
	if keep > 0 {
		prune(dir, keep)
	}
	st, err := os.Stat(final)
	if err != nil {
		return Info{}, err
	}
	return Info{Name: filepath.Base(final), Size: st.Size(), Created: st.ModTime().Unix()}, nil
}

func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	zw, _ := gzip.NewWriterLevel(out, gzip.BestSpeed)
	if _, err := io.Copy(zw, in); err != nil {
		out.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// List returns the backups in dir, newest first.
func List(dir string) ([]Info, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, e := range ents {
		if e.IsDir() || !ValidName(e.Name()) {
			continue
		}
		st, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Info{Name: e.Name(), Size: st.Size(), Created: st.ModTime().Unix()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

func prune(dir string, keep int) {
	list, _ := List(dir)
	for i := keep; i < len(list); i++ {
		os.Remove(filepath.Join(dir, list[i].Name))
	}
}

// Path returns the file path of a named backup.
func Path(dir, name string) (string, error) {
	if !ValidName(name) {
		return "", errors.New("invalid backup name")
	}
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}

// Delete removes a backup.
func Delete(dir, name string) error {
	p, err := Path(dir, name)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// pendingName is the staged restore file inside the data directory.
const pendingName = "restore.pending.db"

// StageRestore validates an uploaded or stored backup (raw SQLite or gzip)
// and stages it; the replacement happens on the next start.
func StageRestore(dataDir string, r io.Reader, maxSchema int) error {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return err
	}
	tmp := filepath.Join(dataDir, pendingName+".part")
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)

	head := make([]byte, 2)
	n, _ := io.ReadFull(r, head)
	rr := io.MultiReader(bytesReader(head[:n]), r)
	if n == 2 && head[0] == 0x1f && head[1] == 0x8b {
		zr, err := gzip.NewReader(rr)
		if err != nil {
			out.Close()
			return fmt.Errorf("not a valid gzip file: %w", err)
		}
		rr = zr
	}
	if _, err := io.Copy(out, io.LimitReader(rr, 64<<30)); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := Verify(tmp, maxSchema); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dataDir, pendingName))
}

type byteReader struct{ b []byte }

func bytesReader(b []byte) io.Reader { return &byteReader{b} }
func (r *byteReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}

// Verify checks that path is a healthy XrayScope database that this build
// understands.
func Verify(path string, maxSchema int) error {
	db, err := sql.Open("sqlite", path+"?_pragma=query_only(1)")
	if err != nil {
		return err
	}
	defer db.Close()
	var res string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&res); err != nil {
		return fmt.Errorf("not a SQLite database: %w", err)
	}
	if res != "ok" {
		return fmt.Errorf("database is damaged: %s", res)
	}
	var v string
	if err := db.QueryRow(`SELECT v FROM kv WHERE k='schema'`).Scan(&v); err != nil {
		return errors.New("this is not an XrayScope backup")
	}
	if n, _ := strconv.Atoi(v); n < 1 || n > maxSchema {
		return fmt.Errorf("backup schema version %s is not supported by this build", v)
	}
	return nil
}

// ApplyPending replaces dbPath with a staged restore, if one exists. The old
// database is kept next to the backups. It reports whether a restore ran.
func ApplyPending(dataDir, dbPath, backupDir string) (bool, error) {
	pending := filepath.Join(dataDir, pendingName)
	if _, err := os.Stat(pending); err != nil {
		return false, nil
	}
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		return false, err
	}
	if _, err := os.Stat(dbPath); err == nil {
		keep := filepath.Join(backupDir, "pre-restore-"+time.Now().Format("20060102-150405")+".db")
		if err := os.Rename(dbPath, keep); err != nil {
			return false, err
		}
	}
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	if err := os.Rename(pending, dbPath); err != nil {
		return false, err
	}
	return true, nil
}

// HasPending reports whether a restore is waiting for a restart.
func HasPending(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, pendingName))
	return err == nil
}
