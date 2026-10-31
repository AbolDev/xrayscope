// Package update finds newer releases on GitHub and replaces the running
// binary with one of them.
package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// DefaultRepo is where releases are published.
const DefaultRepo = "AbolDev/xrayscope"

// Release is one published version.
type Release struct {
	Tag        string `json:"tag"`
	Prerelease bool   `json:"prerelease"`
	Published  string `json:"published"`
	URL        string `json:"url"`
}

type ghRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Published  string `json:"published_at"`
	HTMLURL    string `json:"html_url"`
}

// Releases lists the newest published releases, newest first.
func Releases(ctx context.Context, hc *http.Client, repo string) ([]Release, error) {
	if repo == "" {
		repo = DefaultRepo
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+repo+"/releases?per_page=20", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered HTTP %d", resp.StatusCode)
	}
	var raw []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]Release, 0, len(raw))
	for _, r := range raw {
		if r.Draft || r.Tag == "" {
			continue
		}
		out = append(out, Release{Tag: r.Tag, Prerelease: r.Prerelease, Published: r.Published, URL: r.HTMLURL})
	}
	return out, nil
}

// Pick returns the newest release of a channel ("stable" or "beta"; beta
// includes stable releases).
func Pick(list []Release, channel string) (Release, bool) {
	var best Release
	found := false
	for _, r := range list {
		if channel != "beta" && r.Prerelease {
			continue
		}
		if !found || Newer(r.Tag, best.Tag) {
			best, found = r, true
		}
	}
	return best, found
}

// Newer reports whether version a is newer than b (v1.2.3-beta.4 style).
func Newer(a, b string) bool { return compare(a, b) > 0 }

func compare(a, b string) int {
	ac, ap := split(a)
	bc, bp := split(b)
	for i := 0; i < 3; i++ {
		if ac[i] != bc[i] {
			if ac[i] > bc[i] {
				return 1
			}
			return -1
		}
	}
	switch {
	case ap == "" && bp == "":
		return 0
	case ap == "":
		return 1 // a stable release beats any pre-release of the same version
	case bp == "":
		return -1
	}
	an, bn := preNum(ap), preNum(bp)
	switch {
	case an > bn:
		return 1
	case an < bn:
		return -1
	}
	return 0
}

func split(v string) ([3]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	pre := ""
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}
	var c [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		c[i], _ = strconv.Atoi(p)
	}
	return c, pre
}

func preNum(pre string) int {
	if i := strings.LastIndex(pre, "."); i >= 0 {
		pre = pre[i+1:]
	}
	n, _ := strconv.Atoi(pre)
	return n
}

// Install downloads the release for this platform, checks its checksum and
// replaces the running executable. The process must be restarted afterwards.
func Install(ctx context.Context, hc *http.Client, repo, tag string) error {
	if runtime.GOOS != "linux" {
		return errors.New("self-update is only available on Linux")
	}
	if repo == "" {
		repo = DefaultRepo
	}
	if strings.ContainsAny(tag, "/\\ ?#") || tag == "" {
		return errors.New("invalid version")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/xrayscope-linux-%s.tar.gz", repo, tag, runtime.GOARCH)

	archive, err := os.CreateTemp(filepath.Dir(exe), ".xrayscope-update-*")
	if err != nil {
		return fmt.Errorf("cannot write next to the binary: %w", err)
	}
	defer os.Remove(archive.Name())
	sum := sha256.New()
	if err := fetch(ctx, hc, url, io.MultiWriter(archive, sum)); err != nil {
		archive.Close()
		return fmt.Errorf("download: %w", err)
	}
	var want strings.Builder
	if err := fetch(ctx, hc, url+".sha256", &want); err == nil {
		fields := strings.Fields(want.String())
		if len(fields) > 0 && !strings.EqualFold(fields[0], hex.EncodeToString(sum.Sum(nil))) {
			archive.Close()
			return errors.New("checksum mismatch")
		}
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		archive.Close()
		return err
	}
	next, err := os.CreateTemp(filepath.Dir(exe), ".xrayscope-new-*")
	if err != nil {
		archive.Close()
		return err
	}
	defer os.Remove(next.Name())
	err = extract(archive, next)
	archive.Close()
	if cerr := next.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(next.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(next.Name(), exe)
}

func fetch(ctx context.Context, hc *http.Client, url string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	_, err = io.Copy(w, io.LimitReader(resp.Body, 200<<20))
	return err
}

func extract(r io.Reader, w io.Writer) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("the archive has no xrayscope binary")
		}
		if err != nil {
			return err
		}
		if filepath.Base(h.Name) == "xrayscope" && h.Typeflag == tar.TypeReg {
			_, err := io.Copy(w, io.LimitReader(tr, 200<<20))
			return err
		}
	}
}
