package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"strconv"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		current, candidate string
		want               bool
		valid              bool
	}{
		{"0.1.6", "0.1.7", true, true},
		{"0.1.6", "0.2.0", true, true},
		{"0.1.6", "1.0.0", true, true},
		{"0.1.6", "0.1.6", false, true},
		{"0.1.6", "0.1.5", false, true},
		{"1.2.3", "1.2.3", false, true},
		{"0.9.9", "0.10.0", true, true},
		{"0.1.6", "v0.1.7", false, false},
		{"0.1", "0.1.7", false, false},
		{"0.1.x", "0.1.7", false, false},
	}
	for _, tc := range cases {
		got, err := Newer(tc.current, tc.candidate)
		if tc.valid && err != nil {
			t.Errorf("%s→%s: unexpected error %v", tc.current, tc.candidate, err)
		}
		if !tc.valid && err == nil {
			t.Errorf("%s→%s: expected invalid version error", tc.current, tc.candidate)
		}
		if got != tc.want {
			t.Errorf("%s→%s: got %v want %v", tc.current, tc.candidate, got, tc.want)
		}
	}
}

type fixture struct {
	release   Release
	manifest  Manifest
	installer []byte
	private   ed25519.PrivateKey
	public    string
	server    *httptest.Server
}

func newFixture(t *testing.T, version string, installer []byte) *fixture {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{installer: installer, private: private, public: hex.EncodeToString(public)}
	f.manifest = Manifest{Version: version, Arch: archWindows, File: "AgentTitleManager-setup.exe",
		Size: int64(len(installer)), SHA256: hex.EncodeToString(sha256Sum(installer)), Notes: "修复若干问题"}
	sig, err := Sign(private, f.manifest)
	if err != nil {
		t.Fatal(err)
	}
	f.manifest.Signature = sig
	f.release = Release{TagName: "v" + version, Assets: []Asset{
		{Name: manifestName, Size: 1024, BrowserDownloadURL: "manifest"},
		{Name: f.manifest.File, Size: int64(len(installer)), BrowserDownloadURL: "installer"},
	}, Body: "测试更新说明"}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			release := f.release
			for i := range release.Assets {
				release.Assets[i].BrowserDownloadURL = f.server.URL + "/" + release.Assets[i].BrowserDownloadURL
			}
			_ = json.NewEncoder(w).Encode(release)
		case "/manifest":
			_ = json.NewEncoder(w).Encode(f.manifest)
		case "/installer":
			_, _ = w.Write(f.installer)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fixture) source(t *testing.T) *Source {
	t.Helper()
	s, err := NewSource(f.server.URL+"/release", f.public)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func sha256Sum(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func TestCheckLatest(t *testing.T) {
	installer := []byte("fake-installer-bytes")
	f := newFixture(t, "0.1.7", installer)
	check, err := f.source(t).CheckLatest(context.Background(), "0.1.6")
	if err != nil {
		t.Fatal(err)
	}
	if check.Version != "0.1.7" || check.SHA256 != f.manifest.SHA256 || check.Size != int64(len(installer)) {
		t.Fatalf("unexpected check: %+v", check)
	}
	if check.Notes != "测试更新说明" {
		t.Fatalf("unexpected notes: %q", check.Notes)
	}
}

func TestCheckLatestSilentCases(t *testing.T) {
	installer := []byte("installer")
	cases := []struct {
		name   string
		mutate func(*fixture)
	}{
		{"equal version", func(f *fixture) { f.release.TagName = "v0.1.6"; f.manifest.Version = "0.1.6" }},
		{"older version", func(f *fixture) { f.release.TagName = "v0.1.5"; f.manifest.Version = "0.1.5" }},
		{"draft", func(f *fixture) { f.release.Draft = true }},
		{"prerelease", func(f *fixture) { f.release.Prerelease = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "0.1.7", installer)
			tc.mutate(f)
			_, err := f.source(t).CheckLatest(context.Background(), "0.1.6")
			if !errors.Is(err, ErrNoUpdate) {
				t.Fatalf("expected silent no-update, got %v", err)
			}
		})
	}
}

func TestCheckLatestRejectsBadManifest(t *testing.T) {
	installer := []byte("installer")
	cases := []struct {
		name   string
		mutate func(*fixture)
		want   error
	}{
		{"wrong signature key", func(f *fixture) {
			_, other, _ := ed25519.GenerateKey(rand.Reader)
			sig, _ := Sign(other, f.manifest)
			f.manifest.Signature = sig
		}, ErrUntrusted},
		{"tampered notes", func(f *fixture) { f.manifest.Notes = "被篡改的说明" }, ErrUntrusted},
		{"version mismatch", func(f *fixture) { f.release.TagName = "v9.9.9" }, ErrUntrusted},
		{"asset size mismatch", func(f *fixture) { f.release.Assets[1].Size = 1 }, ErrUntrusted},
		{"wrong arch", func(f *fixture) {
			f.manifest.Arch = "linux-x64"
			sig, _ := Sign(f.private, f.manifest)
			f.manifest.Signature = sig
		}, ErrUntrusted},
		{"path traversal", func(f *fixture) {
			f.manifest.File = `..\evil.exe`
			sig, _ := Sign(f.private, f.manifest)
			f.manifest.Signature = sig
		}, ErrUntrusted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "0.1.7", installer)
			tc.mutate(f)
			_, err := f.source(t).CheckLatest(context.Background(), "0.1.6")
			if tc.want == nil {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

func TestDownload(t *testing.T) {
	installer := []byte("signed-and-verified-installer")
	f := newFixture(t, "0.1.7", installer)
	check, err := f.source(t).CheckLatest(context.Background(), "0.1.6")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	var progressCalls int
	err = f.source(t).Download(context.Background(), check, &out, func(_, _ int64) { progressCalls++ })
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), installer) || progressCalls == 0 {
		t.Fatalf("download mismatch: %q %d", out.String(), progressCalls)
	}
}

func TestDownloadHashFailureKeepsOld(t *testing.T) {
	installer := []byte("original-installer")
	f := newFixture(t, "0.1.7", installer)
	check, err := f.source(t).CheckLatest(context.Background(), "0.1.6")
	if err != nil {
		t.Fatal(err)
	}
	f.installer = []byte("corrupted-in-transit")
	var out bytes.Buffer
	err = f.source(t).Download(context.Background(), check, &out, nil)
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("expected hash mismatch, got %v", err)
	}
	if out.Len() != 0 && !bytes.Equal(out.Bytes(), f.installer) {
		t.Fatalf("destination should be discarded on hash failure: %q", out.String())
	}
}

func TestNetworkFailureIsRetryable(t *testing.T) {
	source, err := NewSource("http://127.0.0.1:1/release", strings.Repeat("0", 64))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		_, err := source.CheckLatest(context.Background(), "0.1.6")
		if err == nil {
			t.Fatal("expected network error")
		}
	}
}

func TestHTTPFailureIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()
	source, err := NewSource(server.URL+"/release", strings.Repeat("0", 64))
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.CheckLatest(context.Background(), "0.1.6")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected HTTP 500 error, got %v", err)
	}
}

func TestVerifyChecksSignatureBeforeTrustingFields(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	manifest := Manifest{Version: "0.1.7", Arch: archWindows, File: "setup.exe", Size: 1, SHA256: strings.Repeat("a", 64)}
	sig, _ := Sign(private, manifest)
	manifest.Signature = sig
	if err := Verify(public, manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Size = -1
	if err := Verify(public, manifest); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("expected untrusted, got %v", err)
	}
}

func TestParsePublicKeyRejectsInvalid(t *testing.T) {
	for _, text := range []string{"", "abc", strings.Repeat("0", 63), strings.Repeat("g", 64)} {
		if _, err := ParsePublicKey(text); err == nil {
			t.Errorf("accepted bad key %q", text)
		}
	}
}

func TestPublicKeyReturnsBuildInjectedValue(t *testing.T) {
	previous := updatePublicKey
	updatePublicKey = strings.Repeat("a", 64)
	defer func() { updatePublicKey = previous }()
	if got := PublicKey(); got != updatePublicKey {
		t.Fatalf("injected public key was not exposed to the helper: %q", got)
	}
}

func TestRetryableDownloadClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"stall", ErrDownloadStalled, true}, {"eof", io.EOF, true}, {"unexpected", io.ErrUnexpectedEOF, true},
		{"network", &net.DNSError{Err: "reset"}, true}, {"500", &httpStatusError{500}, true}, {"429", &httpStatusError{429}, true},
		{"404", &httpStatusError{404}, false}, {"403", &httpStatusError{403}, false}, {"hash", ErrHashMismatch, false}, {"trust", ErrUntrusted, false}, {"cancel", context.Canceled, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RetryableDownload(tc.err); got != tc.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}

func TestNewSourceDownloadTransportDefaults(t *testing.T) {
	f := newFixture(t, "0.1.7", []byte("installer"))
	source := f.source(t)
	transport, ok := source.download.Transport.(*http.Transport)
	if !ok || transport.Proxy == nil || source.download.Timeout != 0 {
		t.Fatalf("transport lost defaults: %#v", source.download)
	}
}

func TestDownloadSlowChunksAndStall(t *testing.T) {
	payload := []byte("slow payload")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		for _, b := range payload { _, _ = w.Write([]byte{b}); if f, ok := w.(http.Flusher); ok { f.Flush() }; time.Sleep(10 * time.Millisecond) }
	}))
	defer srv.Close()
	s := &Source{download: srv.Client(), stallTimeout: 30 * time.Millisecond}
	check := Check{DownloadURL: srv.URL, Size: int64(len(payload)), SHA256: hex.EncodeToString(sha256Sum(payload))}
	var out bytes.Buffer
	if err := s.Download(context.Background(), check, &out, nil); err != nil || !bytes.Equal(out.Bytes(), payload) { t.Fatalf("slow download: %v %q", err, out.Bytes()) }
	stall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("x")); if f, ok := w.(http.Flusher); ok { f.Flush() }; time.Sleep(150 * time.Millisecond) }))
	defer stall.Close()
	s.download = stall.Client(); check.DownloadURL = stall.URL; check.Size = 2; check.SHA256 = hex.EncodeToString(sha256Sum([]byte("xx")))
	if err := s.Download(context.Background(), check, io.Discard, nil); !errors.Is(err, ErrDownloadStalled) { t.Fatalf("stall error: %v", err) }
}
