package payload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestParseList(t *testing.T) {
	items, err := ParseList(strings.NewReader(`
# comment
firefox.msi ` + sum("a") + ` https://example.org/firefox.msi

7zip.msi    ` + strings.ToUpper(sum("b")) + `  https://example.org/7z.msi
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[1].SHA256 != sum("b") || items[0].URL != "https://example.org/firefox.msi" {
		t.Errorf("%+v", items)
	}
}

func TestParseListRejects(t *testing.T) {
	for name, text := range map[string]string{
		"fields":    "a.msi " + sum("a"),
		"short sha": "a.msi abc https://x/a",
		"http":      "a.msi " + sum("a") + " http://x/a",
		"bad name":  "../a.msi " + sum("a") + " https://x/a",
		"duplicate": "a.msi " + sum("a") + " https://x/a\na.msi " + sum("a") + " https://x/a",
	} {
		if _, err := ParseList(strings.NewReader(text)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestEnsure(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/good":
			w.Write([]byte("good payload"))
		case "/tampered":
			w.Write([]byte("not what was pinned"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	f := &Fetcher{Dir: dir, Client: srv.Client()}
	ctx := context.Background()

	good := Item{Name: "good.msi", SHA256: sum("good payload"), URL: srv.URL + "/good"}
	if err := f.Ensure(ctx, []Item{good}); err != nil {
		t.Fatalf("good: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "good.msi")); string(b) != "good payload" {
		t.Errorf("content = %q", b)
	}

	// Already present: verified, not downloaded again.
	f.Client = &http.Client{Transport: failTransport{}}
	if err := f.Ensure(ctx, []Item{good}); err != nil {
		t.Errorf("cached: %v", err)
	}
	f.Client = srv.Client()

	tampered := Item{Name: "evil.msi", SHA256: sum("good payload"), URL: srv.URL + "/tampered"}
	if err := f.Ensure(ctx, []Item{tampered}); err == nil || !strings.Contains(err.Error(), "not kept") {
		t.Errorf("tampered: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.msi")); !os.IsNotExist(err) {
		t.Error("tampered download was kept")
	}

	missing := Item{Name: "gone.msi", SHA256: sum("x"), URL: srv.URL + "/gone"}
	if err := f.Ensure(ctx, []Item{missing}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("404: err = %v", err)
	}

	// A cached file that no longer matches its pin is an error, never replaced.
	os.WriteFile(filepath.Join(dir, "good.msi"), []byte("modified"), 0o644)
	if err := f.Ensure(ctx, []Item{good}); err == nil {
		t.Error("modified cached file accepted")
	}

	if left, _ := filepath.Glob(filepath.Join(dir, "*.part-*")); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

type failTransport struct{}

func (failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("network used for a cached, verified file")
}
