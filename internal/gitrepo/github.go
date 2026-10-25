package gitrepo

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"konnyaku/internal/formats"
)

// HTTP is injectable for tests; requests always target api.github.com.
type Repository struct {
	URL, Branch, Token string
	HTTP               *http.Client
}

type Entry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

func (e Entry) Regular() bool { return e.Type == "blob" && (e.Mode == "100644" || e.Mode == "100755") }

type Snapshot struct {
	Commit string           `json:"commit"`
	Tree   string           `json:"tree"`
	Files  map[string]Entry `json:"-"`
}

func (s Snapshot) Paths() []string {
	paths := make([]string, 0, len(s.Files))
	for p, e := range s.Files {
		if e.Regular() && validPath(p) == nil {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths
}

// BlobSHA uses Git's object framing, not the hash of the raw bytes alone.
func BlobSHA(raw []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d%c", len(raw), 0)
	_, _ = h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}
func validSHA(sha string) bool {
	b, err := hex.DecodeString(sha)
	return err == nil && len(b) == 20
}

type APIError struct {
	Status     int
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("GitHub rate limit: retry after %s", e.RetryAfter.Round(time.Second))
	}
	return fmt.Sprintf("GitHub returned HTTP %d", e.Status)
}
func IsStatus(err error, status int) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == status
}

// Only reads are automatically retried. Publication writes are resumed using
// persisted object SHAs and branch/PR lookup, never blindly repeated.
func (r Repository) request(ctx context.Context, method, endpoint string, in, out any) error {
	repo, err := GitHubRepository(r.URL)
	if err != nil {
		return err
	}
	var raw []byte
	if in != nil {
		raw, err = json.Marshal(in)
		if err != nil {
			return err
		}
	}
	client := r.HTTP
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	safe := *client
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("GitHub redirect refused") }
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, "https://api.github.com/repos/"+repo+endpoint, bytes.NewReader(raw))
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("Content-Type", "application/json")
		if r.Token != "" {
			req.Header.Set("Authorization", "Bearer "+r.Token)
		}
		resp, err := safe.Do(req)
		if err != nil {
			return errors.New("GitHub request failed; retry the operation")
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
		resp.Body.Close()
		if readErr != nil {
			return errors.New("GitHub response interrupted; retry the operation")
		}
		if len(data) > 8<<20 {
			return errors.New("GitHub response too large")
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(data, out); err != nil {
				return errors.New("invalid GitHub response")
			}
			return nil
		}
		apiErr := &APIError{Status: resp.StatusCode}
		delay := time.Duration(attempt+1) * time.Second
		if seconds, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && seconds > 0 {
			apiErr.RetryAfter = time.Duration(seconds) * time.Second
		}
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			if reset, e := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); e == nil {
				if d := time.Until(time.Unix(reset, 0)); d > apiErr.RetryAfter {
					apiErr.RetryAfter = d
				}
			}
		}
		if apiErr.RetryAfter > delay {
			delay = apiErr.RetryAfter
		}
		retry := resp.StatusCode == 429 || resp.StatusCode >= 500 || (resp.StatusCode == 403 && apiErr.RetryAfter > 0)
		if method != http.MethodGet || !retry || attempt == 2 || delay > 10*time.Second {
			return apiErr
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return errors.New("GitHub retries exhausted")
}

func (r Repository) Head(ctx context.Context, branch string) (string, error) {
	if err := ValidateBranch(branch); err != nil {
		return "", err
	}
	var ref struct{ Object struct{ SHA string } }
	if err := r.request(ctx, "GET", "/git/ref/heads/"+url.PathEscape(branch), nil, &ref); err != nil {
		return "", err
	}
	if !validSHA(ref.Object.SHA) {
		return "", errors.New("invalid GitHub commit SHA")
	}
	return ref.Object.SHA, nil
}
func (r Repository) Snapshot(ctx context.Context) (Snapshot, error) {
	sha, err := r.Head(ctx, r.Branch)
	if err != nil {
		return Snapshot{}, err
	}
	return r.At(ctx, sha)
}
func (r Repository) At(ctx context.Context, sha string) (Snapshot, error) {
	s := Snapshot{Commit: sha, Files: map[string]Entry{}}
	if !validSHA(sha) {
		return s, errors.New("invalid commit SHA")
	}
	var commit struct{ Tree struct{ SHA string } }
	if err := r.request(ctx, "GET", "/git/commits/"+sha, nil, &commit); err != nil {
		return s, err
	}
	s.Tree = commit.Tree.SHA
	if !validSHA(s.Tree) {
		return s, errors.New("invalid GitHub tree SHA")
	}
	type tree struct {
		Tree      []Entry
		Truncated bool
	}
	var all tree
	if err := r.request(ctx, "GET", "/git/trees/"+s.Tree+"?recursive=1", nil, &all); err != nil {
		return s, err
	}
	if !all.Truncated {
		for _, e := range all.Tree {
			s.Files[e.Path] = e
		}
		return s, nil
	}
	// A truncated recursive response is incomplete. Traverse non-recursive trees
	// from the root; bound both requests and depth for very large repositories.
	requests := 0
	var walk func(string, string, int) error
	walk = func(sha, prefix string, depth int) error {
		requests++
		if requests > 1000 || depth > 64 {
			return errors.New("repository tree exceeds scan limits")
		}
		var t tree
		if err := r.request(ctx, "GET", "/git/trees/"+sha, nil, &t); err != nil {
			return err
		}
		if t.Truncated {
			return errors.New("GitHub returned an incomplete directory")
		}
		for _, e := range t.Tree {
			e.Path = prefix + e.Path
			s.Files[e.Path] = e
			if len(s.Files) > 200000 {
				return errors.New("repository tree exceeds scan limits")
			}
			if e.Type == "tree" {
				if !validSHA(e.SHA) {
					return errors.New("invalid GitHub tree SHA")
				}
				if err := walk(e.SHA, e.Path+"/", depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return s, walk(s.Tree, "", 0)
}
func (r Repository) Read(ctx context.Context, e Entry) ([]byte, error) {
	if !e.Regular() || !validSHA(e.SHA) || validPath(e.Path) != nil {
		return nil, errors.New("not a regular translation file")
	}
	if e.Size > formats.MaxSize {
		return nil, errors.New("translation file exceeds 4 MiB")
	}
	var blob struct {
		Content, Encoding string
		Size              int64
	}
	if err := r.request(ctx, "GET", "/git/blobs/"+e.SHA, nil, &blob); err != nil {
		return nil, err
	}
	if blob.Encoding != "base64" {
		return nil, errors.New("unsupported GitHub blob encoding")
	}
	raw, err := base64.StdEncoding.DecodeString(blob.Content)
	if err != nil {
		return nil, errors.New("invalid GitHub blob")
	}
	if len(raw) > formats.MaxSize {
		return nil, errors.New("translation file exceeds 4 MiB")
	}
	if BlobSHA(raw) != e.SHA {
		return nil, errors.New("GitHub blob does not match its SHA")
	}
	return raw, nil
}

type Change struct {
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	Before  string `json:"before"`
	Content string `json:"content"`
}

func (r Repository) CreateCommit(ctx context.Context, s Snapshot, changes []Change, title string, at time.Time) (string, error) {
	entries := make([]map[string]string, 0, len(changes))
	for _, c := range changes {
		if validPath(c.Path) != nil || (c.Mode != "100644" && c.Mode != "100755") {
			return "", errors.New("invalid translation path or mode")
		}
		var blob struct{ SHA string }
		if err := r.request(ctx, "POST", "/git/blobs", map[string]string{"content": base64.StdEncoding.EncodeToString([]byte(c.Content)), "encoding": "base64"}, &blob); err != nil {
			return "", err
		}
		if blob.SHA != BlobSHA([]byte(c.Content)) {
			return "", errors.New("invalid created blob SHA")
		}
		entries = append(entries, map[string]string{"path": c.Path, "mode": c.Mode, "type": "blob", "sha": blob.SHA})
	}
	var tree struct{ SHA string }
	if err := r.request(ctx, "POST", "/git/trees", map[string]any{"base_tree": s.Tree, "tree": entries}, &tree); err != nil {
		return "", err
	}
	if !validSHA(tree.SHA) {
		return "", errors.New("invalid created tree SHA")
	}
	identity := map[string]string{"name": "Konnyaku", "email": "konnyaku@localhost", "date": at.UTC().Format(time.RFC3339)}
	var commit struct{ SHA string }
	if err := r.request(ctx, "POST", "/git/commits", map[string]any{"message": title, "tree": tree.SHA, "parents": []string{s.Commit}, "author": identity, "committer": identity}, &commit); err != nil {
		return "", err
	}
	if !validSHA(commit.SHA) {
		return "", errors.New("invalid created commit SHA")
	}
	return commit.SHA, nil
}
func (r Repository) EnsureBranch(ctx context.Context, branch, commit string) error {
	sha, err := r.Head(ctx, branch)
	if err == nil {
		if sha != commit {
			return errors.New("publication branch changed on GitHub")
		}
		return nil
	}
	if !IsStatus(err, 404) {
		return err
	}
	return r.request(ctx, "POST", "/git/refs", map[string]string{"ref": "refs/heads/" + branch, "sha": commit}, nil)
}
func (r Repository) FindPR(ctx context.Context, branch, commit string) (string, error) {
	repo, err := GitHubRepository(r.URL)
	if err != nil {
		return "", err
	}
	params := url.Values{"state": {"all"}, "head": {strings.Split(repo, "/")[0] + ":" + branch}, "base": {r.Branch}, "per_page": {"100"}}
	var prs []struct {
		URL  string               `json:"html_url"`
		Head struct{ SHA string } `json:"head"`
	}
	if err = r.request(ctx, "GET", "/pulls?"+params.Encode(), nil, &prs); err != nil {
		return "", err
	}
	if len(prs) == 0 {
		return "", nil
	}
	if prs[0].Head.SHA != commit {
		return "", errors.New("publication pull request changed on GitHub")
	}
	return r.prURL(prs[0].URL)
}
func (r Repository) prURL(result string) (string, error) {
	repo, err := GitHubRepository(r.URL)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(result)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || !strings.HasPrefix(u.Path, "/"+repo+"/pull/") {
		return "", errors.New("invalid GitHub pull request URL")
	}
	return result, nil
}
func (r Repository) EnsurePR(ctx context.Context, branch, commit, title, body string) (string, error) {
	if r.Token == "" {
		return "", errors.New("GITHUB_TOKEN not configured")
	}
	existing, err := r.FindPR(ctx, branch, commit)
	if err != nil || existing != "" {
		return existing, err
	}
	var pr struct {
		URL string `json:"html_url"`
	}
	if err = r.request(ctx, "POST", "/pulls", map[string]any{"title": title, "body": body, "head": branch, "base": r.Branch, "draft": true}, &pr); err != nil {
		return "", err
	}
	return r.prURL(pr.URL)
}
