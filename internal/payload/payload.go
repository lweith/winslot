// Package payload fetches and verifies the pinned guest payload: the
// installers and drivers that go into every golden image.
//
// The list (assets/payload.list) pins each file by URL and sha256. Nothing
// unverified ever reaches an image: a file whose hash does not match is
// rejected, and a download is only kept once its hash checks out.
package payload

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Item is one pinned file.
type Item struct {
	Name   string // file name in the cache and on the WINCFG CD
	SHA256 string // lowercase hex
	URL    string
}

var (
	nameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	shaRE  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ParseList reads "NAME SHA256 URL" lines; blank lines and #-comments are skipped.
func ParseList(r io.Reader) ([]Item, error) {
	var items []Item
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			return nil, fmt.Errorf("payload list line %d: want NAME SHA256 URL, got %d fields", n, len(f))
		}
		it := Item{Name: f[0], SHA256: strings.ToLower(f[1]), URL: f[2]}
		switch {
		case !nameRE.MatchString(it.Name):
			return nil, fmt.Errorf("payload list line %d: bad file name %q", n, it.Name)
		case !shaRE.MatchString(it.SHA256):
			return nil, fmt.Errorf("payload list line %d: %s: sha256 must be 64 hex digits", n, it.Name)
		case !strings.HasPrefix(it.URL, "https://"):
			return nil, fmt.Errorf("payload list line %d: %s: URL must be https", n, it.Name)
		case seen[it.Name]:
			return nil, fmt.Errorf("payload list line %d: duplicate %s", n, it.Name)
		}
		seen[it.Name] = true
		items = append(items, it)
	}
	return items, sc.Err()
}

// FileSHA256 returns the hex sha256 of the file at path.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Fetcher downloads into a cache directory.
type Fetcher struct {
	Dir    string
	Client *http.Client
	Log    func(format string, args ...any)
}

// Ensure makes every item present in Dir and verified. Existing files are
// checked, not re-downloaded; a mismatching existing file is an error (it is
// never silently replaced).
func (f *Fetcher) Ensure(ctx context.Context, items []Item) error {
	if err := os.MkdirAll(f.Dir, 0o755); err != nil {
		return err
	}
	for _, it := range items {
		path := filepath.Join(f.Dir, it.Name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			f.logf("download %s", it.Name)
			if err := f.download(ctx, it, path); err != nil {
				return err
			}
		}
		got, err := FileSHA256(path)
		if err != nil {
			return err
		}
		if got != it.SHA256 {
			return fmt.Errorf("payload: %s: sha256 %s, pinned %s", it.Name, got, it.SHA256)
		}
		f.logf("ok       %s", it.Name)
	}
	return nil
}

// download writes to a temporary file and renames it into place only after
// its hash matches, so a cancelled or tampered download never looks complete.
func (f *Fetcher) download(ctx context.Context, it Item, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, it.URL, nil)
	if err != nil {
		return err
	}
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("payload: %s: %w", it.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("payload: %s: HTTP %s", it.Name, resp.Status)
	}
	tmp, err := os.CreateTemp(f.Dir, it.Name+".part-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("payload: %s: %w", it.Name, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != it.SHA256 {
		return fmt.Errorf("payload: %s: downloaded sha256 %s, pinned %s (not kept)", it.Name, got, it.SHA256)
	}
	return os.Rename(tmp.Name(), path)
}

func (f *Fetcher) logf(format string, args ...any) {
	if f.Log != nil {
		f.Log(format, args...)
	}
}
