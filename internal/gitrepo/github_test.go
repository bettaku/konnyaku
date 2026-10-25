package gitrepo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(r *http.Request, status int, out any) *http.Response {
	data, _ := json.Marshal(out)
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(data))), Request: r}
}
func TestPinnedSnapshotTruncatedTree(t *testing.T) {
	commit := strings.Repeat("a", 40)
	root := strings.Repeat("b", 40)
	sub := strings.Repeat("c", 40)
	content := strings.Repeat("x", (1<<20)+1)
	blob := BlobSHA([]byte(content))
	requests := []string{}
	r := Repository{URL: "https://github.com/o/r.git", Branch: "release/v1", Token: "secret", HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "api.github.com" || req.Header.Get("Authorization") != "Bearer secret" || req.Header.Get("X-GitHub-Api-Version") == "" {
			t.Error("unexpected request headers/host")
		}
		path := strings.TrimPrefix(req.URL.Path, "/repos/o/r")
		requests = append(requests, path)
		switch path {
		case "/git/ref/heads/release/v1":
			return response(req, 200, map[string]any{"object": map[string]string{"sha": commit}}), nil
		case "/git/commits/" + commit:
			return response(req, 200, map[string]any{"tree": map[string]string{"sha": root}}), nil
		case "/git/trees/" + root:
			if req.URL.Query().Has("recursive") {
				return response(req, 200, map[string]any{"truncated": true, "tree": []Entry{{Path: "ignore-truncated", SHA: blob, Type: "blob", Mode: "100644"}}}), nil
			}
			return response(req, 200, map[string]any{"tree": []Entry{{Path: "locales", SHA: sub, Type: "tree", Mode: "040000"}}}), nil
		case "/git/trees/" + sub:
			return response(req, 200, map[string]any{"tree": []Entry{{Path: "en.json", SHA: blob, Type: "blob", Mode: "100644", Size: int64(len(content))}, {Path: "ja.json", Type: "blob", Mode: "120000", SHA: blob}, {Path: "nested", Type: "commit", Mode: "160000", SHA: blob}}}), nil
		case "/git/blobs/" + blob:
			return response(req, 200, map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content))}), nil
		default:
			t.Fatalf("unexpected request %s", path)
			return nil, nil
		}
	})}}
	snapshot, err := r.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Commit != commit || snapshot.Tree != root || strings.Join(snapshot.Paths(), ",") != "locales/en.json" {
		t.Fatalf("incomplete or unsafe snapshot: %+v", snapshot)
	}
	raw, err := r.Read(context.Background(), snapshot.Files["locales/en.json"])
	if err != nil || string(raw) != content {
		t.Fatalf("large blob: %d %v", len(raw), err)
	}
	if _, err = r.Read(context.Background(), snapshot.Files["locales/ja.json"]); err == nil {
		t.Fatal("symlink was accepted")
	}
	if _, err = r.Read(context.Background(), Entry{Path: "../escape", Type: "blob", Mode: "100644", SHA: blob}); err == nil {
		t.Fatal("traversal was accepted")
	}
	count := 0
	for _, path := range requests {
		if strings.HasPrefix(path, "/git/ref/") {
			count++
		}
	}
	if count != 1 {
		t.Fatal("snapshot re-read a moving branch")
	}
}
func TestGitHubReadBoundsAndIntegrity(t *testing.T) {
	cases := []struct {
		name  string
		entry Entry
		body  any
	}{
		{"declared too large", Entry{Path: "en.json", Type: "blob", Mode: "100644", SHA: strings.Repeat("a", 40), Size: 5 << 20}, nil},
		{"hash mismatch", Entry{Path: "en.json", Type: "blob", Mode: "100644", SHA: strings.Repeat("a", 40)}, map[string]string{"encoding": "base64", "content": "eA=="}},
		{"invalid encoding", Entry{Path: "en.json", Type: "blob", Mode: "100644", SHA: strings.Repeat("a", 40)}, map[string]string{"encoding": "none", "content": ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Repository{URL: "https://github.com/o/r", HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
				if tc.body == nil {
					t.Fatal("oversized file was fetched")
				}
				return response(req, 200, tc.body), nil
			})}}
			if _, err := r.Read(context.Background(), tc.entry); err == nil {
				t.Fatal("invalid blob accepted")
			}
		})
	}
}
func TestRateLimitAndMutationRetry(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		t.Run(method, func(t *testing.T) {
			calls := 0
			r := Repository{URL: "https://github.com/o/r", HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				resp := response(req, 429, map[string]string{"message": "secret provider details"})
				resp.Header.Set("Retry-After", "120")
				return resp, nil
			})}}
			err := r.request(context.Background(), method, "/test", nil, nil)
			if !IsStatus(err, 429) || calls != 1 || strings.Contains(err.Error(), "secret") {
				t.Fatalf("rate limit: %v %d", err, calls)
			}
		})
	}
	calls := 0
	r := Repository{URL: "https://github.com/o/r", HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) { calls++; return response(req, 503, nil), nil })}}
	if err := r.request(context.Background(), "POST", "/test", nil, nil); !IsStatus(err, 503) || calls != 1 {
		t.Fatal("mutation was retried blindly")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := r.request(ctx, "GET", "/test", nil, nil); err != context.DeadlineExceeded {
		t.Fatalf("retry ignored context: %v", err)
	}
}
func TestGitHubRedirectRefused(t *testing.T) {
	calls := 0
	r := Repository{URL: "https://github.com/o/r", Token: "secret", HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		resp := response(req, 302, nil)
		resp.Header.Set("Location", "https://example.com/steal")
		return resp, nil
	})}}
	if _, err := r.Head(context.Background(), "main"); err == nil || calls != 1 {
		t.Fatal("authenticated redirect followed")
	}
}
func TestEnsureBranchDoesNotOverwrite(t *testing.T) {
	calls := 0
	r := Repository{URL: "https://github.com/o/r", HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != "GET" {
			t.Fatal("remote branch was modified")
		}
		return response(req, 200, map[string]any{"object": map[string]string{"sha": strings.Repeat("a", 40)}}), nil
	})}}
	if err := r.EnsureBranch(context.Background(), "publication", strings.Repeat("b", 40)); err == nil || calls != 1 {
		t.Fatal("changed publication branch accepted")
	}
}
