package auth

import (
	"errors"
	"testing"
)

type memKV map[string]string

func (m memKV) KV(k string) (string, bool, error) { v, ok := m[k]; return v, ok, nil }
func (m memKV) SetKV(k, v string) error           { m[k] = v; return nil }

func TestLoginAndVerify(t *testing.T) {
	a, err := New(memKV{})
	if err != nil {
		t.Fatal(err)
	}
	if a.HasAdmin() {
		t.Fatal("fresh store must have no admin")
	}
	if err := a.SetAdmin("admin", "short"); err == nil {
		t.Fatal("short password accepted")
	}
	if err := a.SetAdmin("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	tok, err := a.Login("admin", "correct horse", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if u, ok := a.Verify(tok); !ok || u != "admin" {
		t.Fatalf("verify = %q %v", u, ok)
	}
	if _, ok := a.Verify(tok + "x"); ok {
		t.Fatal("tampered token accepted")
	}
	if _, err := a.Login("admin", "wrong", "1.1.1.1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong password: %v", err)
	}

	// changing the password invalidates old sessions
	if err := a.ChangePassword("correct horse", "another long one"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Verify(tok); ok {
		t.Fatal("old session survived a password change")
	}
}

func TestBruteForceLock(t *testing.T) {
	a, _ := New(memKV{})
	_ = a.SetAdmin("admin", "correct horse")
	for i := 0; i < maxFails; i++ {
		a.Login("admin", "bad", "9.9.9.9")
	}
	if _, err := a.Login("admin", "correct horse", "9.9.9.9"); !errors.Is(err, ErrLocked) {
		t.Fatalf("expected lock, got %v", err)
	}
	if _, err := a.Login("admin", "correct horse", "8.8.8.8"); err != nil {
		t.Fatalf("other address must not be locked: %v", err)
	}
}
