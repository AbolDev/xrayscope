package backup_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xrayscope/internal/backup"
	"xrayscope/internal/model"
	"xrayscope/internal/store"
)

func TestBackupCycle(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	s, err := store.Open(filepath.Join(data, "xrayscope.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	m := time.Now().Unix() / 60
	if _, err := s.ApplyBatch(0, []model.Rec{{M: m, U: "alice", IP: "1.1.1.1", D: "chatgpt.com", N: 4}}, nil, 1); err != nil {
		t.Fatal(err)
	}

	bdir := filepath.Join(data, "backups")
	info, err := backup.Create(context.Background(), s, bdir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !backup.ValidName(info.Name) || info.Size == 0 {
		t.Fatalf("info = %+v", info)
	}
	list, _ := backup.List(bdir)
	if len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}
	if backup.ValidName("../../etc/passwd") || backup.ValidName("xrayscope-1.db.gz") {
		t.Fatal("unsafe names must be rejected")
	}

	// change live data, then restore the backup over it
	if _, err := s.ApplyBatch(0, []model.Rec{{M: m, U: "bob", IP: "2.2.2.2", D: "x.com", N: 1}}, nil, 1); err != nil {
		t.Fatal(err)
	}
	p, _ := backup.Path(bdir, info.Name)
	f, _ := os.Open(p)
	if err := backup.StageRestore(data, f, 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if !backup.HasPending(data) {
		t.Fatal("restore not staged")
	}
	s.Close()
	ran, err := backup.ApplyPending(data, filepath.Join(data, "xrayscope.db"), bdir)
	if err != nil || !ran {
		t.Fatalf("apply: %v %v", ran, err)
	}
	s2, err := store.Open(filepath.Join(data, "xrayscope.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	users, _ := s2.Users(context.Background())
	if len(users) != 1 || users[0].Email != "alice" {
		t.Fatalf("restored users = %+v", users)
	}
}

func TestStageRestoreRejectsGarbage(t *testing.T) {
	data := t.TempDir()
	if err := backup.StageRestore(data, strings.NewReader("this is not a database"), 1); err == nil {
		t.Fatal("garbage accepted")
	}
	if backup.HasPending(data) {
		t.Fatal("rejected upload left a pending restore")
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"xrayscope-20240101-000000.db.gz", "xrayscope-20240102-000000.db.gz", "xrayscope-20240103-000000.db.gz"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600)
	}
	s, _ := store.Open(filepath.Join(t.TempDir(), "a.db"), nil)
	defer s.Close()
	if _, err := backup.Create(context.Background(), s, dir, 2); err != nil {
		t.Fatal(err)
	}
	list, _ := backup.List(dir)
	if len(list) != 2 {
		t.Fatalf("after prune: %+v", list)
	}
}
