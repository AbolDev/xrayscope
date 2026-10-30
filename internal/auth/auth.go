// Package auth implements the single administrator account of the web panel
// with signed, stateless session tokens.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// KV is the persistence the package needs.
type KV interface {
	KV(key string) (string, bool, error)
	SetKV(key, val string) error
}

const (
	keyUser   = "auth:user"
	keyHash   = "auth:hash"
	keySecret = "auth:secret"
	keyVer    = "auth:ver"

	// SessionTTL is how long a login stays valid.
	SessionTTL = 14 * 24 * time.Hour

	maxFails   = 6
	failWindow = 10 * time.Minute
)

// Auth manages credentials and sessions.
type Auth struct {
	kv KV

	mu     sync.Mutex
	secret []byte
	dummy  []byte // valid bcrypt hash used to equalise timing for unknown users
	fails  map[string]*failState
}

type failState struct {
	n     int
	first time.Time
}

// ErrLocked is returned when too many failed logins came from one address.
var ErrLocked = errors.New("too many failed attempts, try again later")

// ErrInvalid is returned for wrong credentials.
var ErrInvalid = errors.New("invalid username or password")

// New loads (or creates) the signing secret.
func New(kv KV) (*Auth, error) {
	a := &Auth{kv: kv, fails: map[string]*failState{}}
	a.dummy, _ = bcrypt.GenerateFromPassword([]byte(GeneratePassword(16)), 10)
	s, ok, err := kv.KV(keySecret)
	if err != nil {
		return nil, err
	}
	if ok {
		a.secret, _ = hex.DecodeString(s)
	}
	if len(a.secret) != 32 {
		a.secret = make([]byte, 32)
		if _, err := rand.Read(a.secret); err != nil {
			return nil, err
		}
		if err := kv.SetKV(keySecret, hex.EncodeToString(a.secret)); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// HasAdmin reports whether credentials exist.
func (a *Auth) HasAdmin() bool {
	_, ok, _ := a.kv.KV(keyHash)
	return ok
}

// SetAdmin creates or replaces the administrator credentials and invalidates
// every existing session.
func (a *Auth) SetAdmin(user, password string) error {
	user = strings.TrimSpace(user)
	if user == "" || len(user) > 64 {
		return errors.New("username must be 1-64 characters")
	}
	if len(password) < 8 {
		return errors.New("password must have at least 8 characters")
	}
	if len(password) > 72 {
		return errors.New("password must have at most 72 characters")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return err
	}
	ver := a.version() + 1
	if err := a.kv.SetKV(keyUser, user); err != nil {
		return err
	}
	if err := a.kv.SetKV(keyHash, string(h)); err != nil {
		return err
	}
	return a.kv.SetKV(keyVer, strconv.Itoa(ver))
}

// Username returns the administrator's name.
func (a *Auth) Username() string {
	u, _, _ := a.kv.KV(keyUser)
	return u
}

func (a *Auth) version() int {
	v, _, _ := a.kv.KV(keyVer)
	n, _ := strconv.Atoi(v)
	return n
}

// GeneratePassword returns a random password of n characters.
func GeneratePassword(n int) string {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// Login checks the credentials and returns a session token. remote is used
// for brute-force protection.
func (a *Auth) Login(user, password, remote string) (string, error) {
	if err := a.checkLock(remote); err != nil {
		return "", err
	}
	storedUser, _, _ := a.kv.KV(keyUser)
	hash, ok, _ := a.kv.KV(keyHash)
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(storedUser)) == 1
	// always run bcrypt so timing does not reveal whether the user exists
	if !ok {
		hash = string(a.dummy)
	}
	pwOK := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	if !(ok && userOK && pwOK) {
		a.recordFail(remote)
		return "", ErrInvalid
	}
	a.clearFails(remote)
	return a.issue(storedUser), nil
}

func (a *Auth) checkLock(remote string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.fails[remote]
	if f == nil {
		return nil
	}
	if time.Since(f.first) > failWindow {
		delete(a.fails, remote)
		return nil
	}
	if f.n >= maxFails {
		return ErrLocked
	}
	return nil
}

func (a *Auth) recordFail(remote string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.fails[remote]
	if f == nil || time.Since(f.first) > failWindow {
		f = &failState{first: time.Now()}
		a.fails[remote] = f
	}
	f.n++
	if len(a.fails) > 10000 { // bound memory under attack
		for k, v := range a.fails {
			if time.Since(v.first) > failWindow {
				delete(a.fails, k)
			}
		}
	}
}

func (a *Auth) clearFails(remote string) {
	a.mu.Lock()
	delete(a.fails, remote)
	a.mu.Unlock()
}

type claims struct {
	U   string `json:"u"`
	Exp int64  `json:"e"`
	Ver int    `json:"v"`
}

func (a *Auth) issue(user string) string {
	b, _ := json.Marshal(claims{U: user, Exp: time.Now().Add(SessionTTL).Unix(), Ver: a.version()})
	p := base64.RawURLEncoding.EncodeToString(b)
	return p + "." + a.sign(p)
}

func (a *Auth) sign(p string) string {
	m := hmac.New(sha256.New, a.secret)
	m.Write([]byte(p))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Verify validates a session token and returns the username.
func (a *Auth) Verify(token string) (string, bool) {
	p, sig, ok := strings.Cut(token, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(a.sign(p))) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return "", false
	}
	var c claims
	if json.Unmarshal(raw, &c) != nil || c.Exp < time.Now().Unix() || c.Ver != a.version() {
		return "", false
	}
	return c.U, true
}

// ChangePassword verifies the old password and sets the new one.
func (a *Auth) ChangePassword(old, new string) error {
	hash, ok, _ := a.kv.KV(keyHash)
	if !ok || bcrypt.CompareHashAndPassword([]byte(hash), []byte(old)) != nil {
		return ErrInvalid
	}
	return a.SetAdmin(a.Username(), new)
}
