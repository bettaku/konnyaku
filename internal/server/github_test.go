package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"konnyaku/internal/gitrepo"
)

// All server tests use this transport; none can accidentally call real GitHub.
type githubMock struct {
	mu             sync.Mutex
	Files          map[string]string
	Head, Tree     string
	Trees          map[string][]gitrepo.Entry
	Blobs          map[string]string
	Commits        map[string]string
	Branches       map[string]string
	PRs            map[string]string
	Reads, Writes  map[string]int
	LosePRResponse bool
}

func mockGitHub(s *Server, files map[string]string) *githubMock {
	m := &githubMock{Files: files, Trees: map[string][]gitrepo.Entry{}, Blobs: map[string]string{}, Commits: map[string]string{}, Branches: map[string]string{}, PRs: map[string]string{}, Reads: map[string]int{}, Writes: map[string]int{}}
	m.refresh()
	s.GitHubHTTP = &http.Client{Transport: m}
	return m
}
func (m *githubMock) refresh() {
	m.mu.Lock()
	defer m.mu.Unlock()
	var entries []gitrepo.Entry
	for path, raw := range m.Files {
		sha := gitrepo.BlobSHA([]byte(raw))
		m.Blobs[sha] = raw
		entries = append(entries, gitrepo.Entry{Path: path, SHA: sha, Mode: "100644", Type: "blob", Size: int64(len(raw))})
	}
	// JSON map ordering is stable even though the tree slice ordering is not.
	data, _ := json.Marshal(m.Files)
	m.Tree = gitrepo.BlobSHA(data)
	m.Trees[m.Tree] = entries
	m.Head = gitrepo.BlobSHA([]byte("commit:" + m.Tree))
	m.Commits[m.Head] = m.Tree
	m.Branches["main"] = m.Head
}
func (m *githubMock) RoundTrip(req *http.Request) (*http.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	path := strings.TrimPrefix(req.URL.Path, "/repos/owner/repo")
	status := 200
	var out any
	if req.Method == "GET" {
		m.Reads[path]++
	} else {
		m.Writes[path]++
	}
	var in map[string]json.RawMessage
	if req.Body != nil {
		defer req.Body.Close()
		_ = json.NewDecoder(req.Body).Decode(&in)
	}
	str := func(k string) string { var s string; _ = json.Unmarshal(in[k], &s); return s }
	switch {
	case req.Method == "GET" && strings.HasPrefix(path, "/git/ref/heads/"):
		sha := m.Branches[strings.TrimPrefix(path, "/git/ref/heads/")]
		if sha == "" {
			status = 404
		} else {
			out = map[string]any{"object": map[string]string{"sha": sha}}
		}
	case req.Method == "GET" && strings.HasPrefix(path, "/git/commits/"):
		tree, ok := m.Commits[strings.TrimPrefix(path, "/git/commits/")]
		if !ok {
			status = 404
		} else {
			out = map[string]any{"tree": map[string]string{"sha": tree}}
		}
	case req.Method == "GET" && strings.HasPrefix(path, "/git/trees/"):
		tree, ok := m.Trees[strings.TrimPrefix(path, "/git/trees/")]
		if !ok {
			status = 404
		} else {
			out = map[string]any{"tree": tree, "truncated": false}
		}
	case req.Method == "GET" && strings.HasPrefix(path, "/git/blobs/"):
		raw, ok := m.Blobs[strings.TrimPrefix(path, "/git/blobs/")]
		if !ok {
			status = 404
		} else {
			out = map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(raw)), "encoding": "base64", "size": len(raw)}
		}
	case req.Method == "POST" && path == "/git/blobs":
		raw, err := base64.StdEncoding.DecodeString(str("content"))
		if err != nil {
			return nil, err
		}
		sha := gitrepo.BlobSHA(raw)
		m.Blobs[sha] = string(raw)
		out = map[string]string{"sha": sha}
		status = 201
	case req.Method == "POST" && path == "/git/trees":
		var entries []gitrepo.Entry
		_ = json.Unmarshal(in["tree"], &entries)
		base, ok := m.Trees[str("base_tree")]
		if !ok {
			return nil, errors.New("missing base_tree")
		}
		combined := map[string]gitrepo.Entry{}
		for _, e := range base {
			combined[e.Path] = e
		}
		for _, e := range entries {
			combined[e.Path] = e
		}
		data, _ := json.Marshal(combined)
		sha := gitrepo.BlobSHA(data)
		all := []gitrepo.Entry{}
		for _, e := range combined {
			all = append(all, e)
		}
		m.Trees[sha] = all
		out = map[string]string{"sha": sha}
		status = 201
	case req.Method == "POST" && path == "/git/commits":
		var parents []string
		_ = json.Unmarshal(in["parents"], &parents)
		if len(parents) != 1 || m.Commits[parents[0]] == "" {
			return nil, errors.New("missing parent")
		}
		data, _ := json.Marshal(in)
		sha := gitrepo.BlobSHA(data)
		m.Commits[sha] = str("tree")
		out = map[string]string{"sha": sha}
		status = 201
	case req.Method == "POST" && path == "/git/refs":
		branch := strings.TrimPrefix(str("ref"), "refs/heads/")
		if m.Branches[branch] != "" {
			status = 422
		} else {
			m.Branches[branch] = str("sha")
			out = map[string]string{}
			status = 201
		}
	case req.Method == "GET" && path == "/pulls":
		branch := strings.TrimPrefix(req.URL.Query().Get("head"), "owner:")
		out = []map[string]string{}
		if u := m.PRs[branch]; u != "" {
			out = []map[string]any{{"html_url": u, "head": map[string]string{"sha": m.Branches[branch]}}}
		}
	case req.Method == "POST" && path == "/pulls":
		branch := str("head")
		if m.Branches[branch] == "" {
			return nil, errors.New("missing publication branch")
		}
		u := "https://github.com/owner/repo/pull/1"
		m.PRs[branch] = u
		if m.LosePRResponse {
			m.LosePRResponse = false
			return nil, errors.New("simulated lost response after creating PR")
		}
		out = map[string]string{"html_url": u}
		status = 201
	default:
		return nil, errors.New("unexpected GitHub request: " + req.Method + " " + path)
	}
	data, _ := json.Marshal(out)
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(data))), Request: req}, nil
}

func TestAPIPublicationRecoveryAndConflicts(t *testing.T) {
	s, ts := newTestServer(t)
	remote := mockGitHub(s, map[string]string{"locales/en.json": `{"hello":"Hello"}`, "locales/ja.json": `{"hello":"こんにちは"}`, "README.md": "keep me"})
	s.Config.GitHubToken = "test-token"
	admin := login(t, ts.URL, "admin@example.com", "admin-password-123")
	p := admin.must(201, "POST", "/api/projects", map[string]string{"slug": "api", "name": "API", "source_locale": "en-US"})
	pid := int64(p["id"].(float64))
	ppath := "/api/projects/" + itoa(pid)
	r := admin.must(201, "POST", ppath+"/repositories", map[string]string{"url": "https://github.com/owner/repo", "branch": "main"})
	rid := int64(r["id"].(float64))
	rpath := "/api/repositories/" + itoa(rid)
	co := admin.must(201, "POST", ppath+"/components", map[string]any{"slug": "web", "name": "Web", "format": "json", "repository_id": rid, "file_pattern": "locales/{locale}.json"})
	cid := int64(co["id"].(float64))
	admin.must(409, "POST", rpath+"/preview", map[string]string{})
	admin.must(200, "POST", rpath+"/sync", map[string]string{})
	var unit, version int64
	if err := s.Pool.QueryRow(context.Background(), "SELECT u.id,t.version FROM units u JOIN translations t ON t.unit_id=u.id WHERE u.component_id=$1 AND t.locale='ja'", cid).Scan(&unit, &version); err != nil {
		t.Fatal(err)
	}
	admin.must(200, "PUT", "/api/units/"+itoa(unit)+"/translations/ja", map[string]any{"value": "やあ", "status": "reviewed", "version": version})
	beforeReads := remote.Reads["/git/blobs/"+gitrepo.BlobSHA([]byte(remote.Files["locales/ja.json"]))]
	admin.must(200, "POST", rpath+"/sync", map[string]string{})
	var value, status string
	if err := s.Pool.QueryRow(context.Background(), "SELECT value,status FROM translations WHERE unit_id=$1 AND locale='ja'", unit).Scan(&value, &status); err != nil {
		t.Fatal(err)
	}
	if value != "やあ" || status != "reviewed" {
		t.Fatalf("unchanged sync overwrote local edits: %s %s", value, status)
	}
	if remote.Reads["/git/blobs/"+gitrepo.BlobSHA([]byte(remote.Files["locales/ja.json"]))] != beforeReads {
		t.Fatal("unchanged blob fetched again")
	}
	preview := admin.must(200, "POST", rpath+"/preview", map[string]string{"title": "Translations"})
	job := int64(preview["id"].(float64))
	again := admin.must(200, "POST", rpath+"/preview", map[string]string{"title": "Translations"})
	if again["id"] != preview["id"] {
		t.Fatal("identical preview must reuse its job")
	}
	// A stale preview may not start publishing after even an unrelated remote update.
	remote.Files["README.md"] = "changed remotely"
	remote.refresh()
	admin.must(409, "POST", rpath+"/pull-request", map[string]int64{"publication_id": job})
	if len(remote.Writes) != 0 {
		t.Fatalf("stale preview wrote remotely: %v", remote.Writes)
	}
	preview = admin.must(200, "POST", rpath+"/preview", map[string]string{"title": "Translations"})
	job = int64(preview["id"].(float64))
	remote.LosePRResponse = true
	admin.must(409, "POST", rpath+"/pull-request", map[string]int64{"publication_id": job})
	if remote.Writes["/pulls"] != 1 {
		t.Fatal("PR should have been created once")
	}
	// Simulate a restart and a moving base after GitHub accepted the PR.
	remote.Files["README.md"] = "advanced again"
	remote.refresh()
	s2 := New(s.Pool, s.Config)
	s2.GitHubHTTP = s.GitHubHTTP
	restarted := httptest.NewServer(s2.Echo)
	defer restarted.Close()
	admin = login(t, restarted.URL, "admin@example.com", "admin-password-123")
	result := admin.must(201, "POST", rpath+"/pull-request", map[string]int64{"publication_id": job})
	if result["url"] != "https://github.com/owner/repo/pull/1" || remote.Writes["/pulls"] != 1 || remote.Writes["/git/refs"] != 1 {
		t.Fatalf("resume duplicated publication: %v %v", result, remote.Writes)
	}
	admin.must(201, "POST", rpath+"/pull-request", map[string]int64{"publication_id": job})
	var payload string
	if err := s.Pool.QueryRow(context.Background(), "SELECT changes::text FROM repository_publications WHERE id=$1", job).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if payload != "[]" {
		t.Fatal("completed publication retained file copies")
	}
	commit := result["commit"].(string)
	tree := remote.Trees[remote.Commits[commit]]
	found := map[string]string{}
	for _, e := range tree {
		found[e.Path] = remote.Blobs[e.SHA]
	}
	if found["README.md"] != "changed remotely" || !strings.Contains(found["locales/ja.json"], "やあ") || found["locales/en.json"] != remote.Files["locales/en.json"] {
		t.Fatalf("incorrect published tree: %v", found)
	}
	// Changed remote translations must be synchronized, not overwritten by preview.
	remote.Files["locales/ja.json"] = `{"hello":"別の変更"}`
	remote.refresh()
	admin.must(409, "POST", rpath+"/preview", map[string]string{})
	// Publishers are admins; project managers cannot bypass the UI restriction.
	var uid int64
	if err := s.Pool.QueryRow(context.Background(), "SELECT id FROM users WHERE email='trans@example.com'").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	admin.must(204, "PUT", ppath+"/members/"+itoa(uid), map[string]string{"role": "manager"})
	trans := login(t, ts.URL, "trans@example.com", "translator-pass-1")
	trans.must(403, "POST", rpath+"/preview", map[string]string{})
	trans.must(403, "POST", rpath+"/pull-request", map[string]int64{"publication_id": job})
	trans.must(403, "GET", rpath+"/publications/"+itoa(job), nil)
	admin.must(410, "POST", rpath+"/git/push", map[string]string{})
}

func TestSourceChangeReevaluatesCachedTargets(t *testing.T) {
	s, ts := newTestServer(t)
	remote := mockGitHub(s, map[string]string{"locales/en.json": `{"hello":"Hello"}`, "locales/ja.json": `{"hello":"こんにちは","new":"新しい"}`})
	admin := login(t, ts.URL, "admin@example.com", "admin-password-123")
	p := admin.must(201, "POST", "/api/projects", map[string]string{"slug": "source", "name": "Source", "source_locale": "en-US"})
	ppath := "/api/projects/" + itoa(int64(p["id"].(float64)))
	r := admin.must(201, "POST", ppath+"/repositories", map[string]string{"url": "https://github.com/owner/repo"})
	rid := int64(r["id"].(float64))
	rpath := "/api/repositories/" + itoa(rid)
	co := admin.must(201, "POST", ppath+"/components", map[string]any{"slug": "web", "name": "Web", "format": "json", "repository_id": rid, "file_pattern": "locales/{locale}.json"})
	cid := int64(co["id"].(float64))
	admin.must(200, "POST", rpath+"/sync", map[string]string{})
	if _, err := s.Pool.Exec(context.Background(), "UPDATE translations SET value='local edit',status='reviewed' WHERE locale='ja'"); err != nil {
		t.Fatal(err)
	}
	targetBlob := gitrepo.BlobSHA([]byte(remote.Files["locales/ja.json"]))
	remote.Files["locales/en.json"] = `{"hello":"Hello again","new":"New"}`
	remote.refresh()
	admin.must(200, "POST", rpath+"/sync", map[string]string{})
	if remote.Reads["/git/blobs/"+targetBlob] != 1 {
		t.Fatal("unchanged target was re-downloaded")
	}
	rows, err := s.Pool.Query(context.Background(), "SELECT u.key,t.value,t.status FROM units u JOIN translations t ON t.unit_id=u.id WHERE u.component_id=$1 AND t.locale='ja' ORDER BY u.key", cid)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for rows.Next() {
		var key, value, status string
		if err := rows.Scan(&key, &value, &status); err != nil {
			t.Fatal(err)
		}
		got[key] = value + ":" + status
	}
	rows.Close()
	if got["/hello"] != "local edit:needs_review" || got["/new"] != "新しい:translated" {
		t.Fatalf("cached reevaluation: %v", got)
	}
	// A failed import must leave both its document and baseline at the prior SHA.
	remote.Files["locales/ja.json"] = `{"broken":`
	remote.refresh()
	res := admin.must(200, "POST", rpath+"/sync", map[string]string{})
	if len(res["sync"].(map[string]any)["errors"].([]any)) != 1 {
		t.Fatalf("expected partial failure: %v", res)
	}
	var stored string
	if err := s.Pool.QueryRow(context.Background(), "SELECT blob_sha FROM repository_imports WHERE component_id=$1 AND locale='ja'", cid).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != targetBlob {
		t.Fatal("failed import advanced publication baseline")
	}
	admin.must(409, "POST", rpath+"/preview", map[string]string{})
}

func TestWebhookCoalescesQueuedEvents(t *testing.T) {
	s, ts := newTestServer(t)
	remote := mockGitHub(s, map[string]string{"locales/en.json": `{"a":"A"}`, "locales/ja.json": `{"a":"訳"}`})
	admin := login(t, ts.URL, "admin@example.com", "admin-password-123")
	p := admin.must(201, "POST", "/api/projects", map[string]string{"slug": "queue", "name": "Queue", "source_locale": "en-US"})
	ppath := "/api/projects/" + itoa(int64(p["id"].(float64)))
	r := admin.must(201, "POST", ppath+"/repositories", map[string]string{"url": "https://github.com/owner/repo"})
	rid := int64(r["id"].(float64))
	admin.must(201, "POST", ppath+"/components", map[string]any{"slug": "web", "name": "Web", "format": "json", "repository_id": rid, "file_pattern": "locales/{locale}.json"})
	if _, err := s.Pool.Exec(context.Background(), `INSERT INTO webhook_deliveries(delivery_id,repository_url,ref) VALUES
 ('a','https://github.com/owner/repo.git','refs/heads/main'),('b','https://github.com/owner/repo.git','refs/heads/main'),('c','https://github.com/owner/repo.git','refs/heads/main')`); err != nil {
		t.Fatal(err)
	}
	if err := s.processDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	var done int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM webhook_deliveries WHERE status='done'").Scan(&done); err != nil {
		t.Fatal(err)
	}
	if done != 3 || remote.Reads["/git/ref/heads/main"] != 1 {
		t.Fatalf("coalescing: done=%d reads=%v", done, remote.Reads)
	}
}

func TestPublicationRejectsOverlappingSource(t *testing.T) {
	s, ts := newTestServer(t)
	remote := mockGitHub(s, map[string]string{"en/en.json": `{"a":"Source B"}`, "en/ja.json": `{"a":"Source A"}`, "ja/ja.json": `{"a":"訳"}`})
	admin := login(t, ts.URL, "admin@example.com", "admin-password-123")
	p := admin.must(201, "POST", "/api/projects", map[string]string{"slug": "overlap", "name": "Overlap", "source_locale": "en-US"})
	ppath := "/api/projects/" + itoa(int64(p["id"].(float64)))
	r := admin.must(201, "POST", ppath+"/repositories", map[string]string{"url": "https://github.com/owner/repo"})
	rid := int64(r["id"].(float64))
	rpath := "/api/repositories/" + itoa(rid)
	admin.must(201, "POST", ppath+"/components", map[string]any{"slug": "a", "name": "A", "format": "json", "repository_id": rid, "file_pattern": "{locale}/ja.json"})
	admin.must(201, "POST", ppath+"/components", map[string]any{"slug": "b", "name": "B", "format": "json", "repository_id": rid, "file_pattern": "en/{locale}.json"})
	admin.must(200, "POST", rpath+"/sync", map[string]string{})
	res := admin.must(409, "POST", rpath+"/preview", map[string]string{})
	if !strings.Contains(res["error"].(string), "source file") || len(remote.Writes) > 0 {
		t.Fatalf("source overlap not rejected: %v", res)
	}
}
