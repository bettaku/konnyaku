package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	migrations "konnyaku/db"
	"konnyaku/internal/config"
	"konnyaku/internal/db"
)

const secret = "0123456789abcdef0123456789abcdef"

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	control, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("konnyaku_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = control.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		control.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer control.Close()
		if _, err := control.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	pc, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pc.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var version int
	if err = pool.QueryRow(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 180000 || version >= 190000 {
		t.Fatalf("PostgreSQL 18 required, got %d", version)
	}
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("migration must be idempotent: %v", err)
	}
	cfg := config.Config{Address: "127.0.0.1:0", DatabaseURL: dsn, PublicURL: "http://konnyaku.test", RepositoryRoot: t.TempDir(), WebhookSecret: secret}
	s := New(pool, cfg)
	ts := httptest.NewServer(s.Echo)
	t.Cleanup(ts.Close)
	if _, err = CreateUser(ctx, db.New(pool), "admin@example.com", "admin-password-123", "Admin", true); err != nil {
		t.Fatal(err)
	}
	if _, err = CreateUser(ctx, db.New(pool), "trans@example.com", "translator-pass-1", "Trans", false); err != nil {
		t.Fatal(err)
	}
	return s, ts
}

type client struct {
	t      *testing.T
	base   string
	cookie *http.Cookie
}

func (c *client) do(method, path string, body any, headers map[string]string) (int, []byte) {
	c.t.Helper()
	var reader io.Reader
	ct := "application/json"
	switch b := body.(type) {
	case nil:
	case []byte:
		reader = bytes.NewReader(b)
		ct = "application/octet-stream"
	default:
		raw, _ := json.Marshal(b)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, c.base+path, reader)
	if reader != nil {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("X-Requested-With", "konnyaku")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	for _, ck := range res.Cookies() {
		if ck.Name == "session" {
			c.cookie = ck
		}
	}
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, raw
}
func (c *client) must(code int, method, path string, body any) map[string]any {
	c.t.Helper()
	got, raw := c.do(method, path, body, nil)
	if got != code {
		c.t.Fatalf("%s %s: want %d got %d: %s", method, path, code, got, raw)
	}
	var out map[string]any
	if len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}
func login(t *testing.T, base, email, password string) *client {
	c := &client{t: t, base: base}
	c.must(200, "POST", "/api/login", map[string]string{"email": email, "password": password})
	return c
}

func TestEndToEnd(t *testing.T) {
	_, ts := newTestServer(t)
	anon := &client{t: t, base: ts.URL}
	if code, _ := anon.do("GET", "/api/me", nil, nil); code != 401 {
		t.Fatalf("anonymous /me: %d", code)
	}
	if code, _ := anon.do("POST", "/api/login", map[string]string{"email": "admin@example.com", "password": "wrong-password-xx"}, nil); code != 401 {
		t.Fatalf("bad password: %d", code)
	}
	admin := login(t, ts.URL, "admin@example.com", "admin-password-123")
	trans := login(t, ts.URL, "trans@example.com", "translator-pass-1")

	// CSRF guards.
	req, _ := http.NewRequest("POST", ts.URL+"/api/locales", strings.NewReader(`{"code":"en","name":"English"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin.cookie)
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 403 {
		t.Fatalf("missing header should be 403: %v %v", res.StatusCode, err)
	}
	if code, _ := admin.do("POST", "/api/locales", map[string]string{"code": "en", "name": "English"}, map[string]string{"Origin": "http://evil.test"}); code != 403 {
		t.Fatalf("foreign origin should be 403: %d", code)
	}

	admin.must(200, "POST", "/api/locales", map[string]string{"code": "en", "name": "English"})
	admin.must(200, "POST", "/api/locales", map[string]string{"code": "ja", "name": "Japanese"})
	admin.must(400, "POST", "/api/locales", map[string]string{"code": "not a locale!", "name": "x"})
	trans.must(403, "POST", "/api/locales", map[string]string{"code": "fr", "name": "French"})

	p := admin.must(201, "POST", "/api/projects", map[string]string{"slug": "demo", "name": "Demo", "source_locale": "en"})
	admin.must(409, "POST", "/api/projects", map[string]string{"slug": "demo", "name": "Demo", "source_locale": "en"})
	pid := int64(p["id"].(float64))
	ppath := "/api/projects/" + itoa(pid)
	// Translator is not a member yet: project must be invisible.
	trans.must(404, "GET", ppath+"/components", nil)
	admin.must(204, "PUT", ppath+"/members/2", map[string]string{"role": "translator"})
	trans.must(200, "GET", ppath+"/components", nil)
	trans.must(403, "GET", ppath+"/members", nil)

	co := admin.must(201, "POST", ppath+"/components", map[string]string{"slug": "web", "name": "Web", "format": "json"})
	cpath := "/api/components/" + itoa(int64(co["id"].(float64)))
	if fp := co["file_pattern"]; fp != "locales/{locale}.json" {
		t.Fatalf("default pattern: %v", fp)
	}
	trans.must(403, "POST", ppath+"/components", map[string]string{"slug": "x", "name": "X", "format": "json"})
	trans.must(403, "POST", cpath+"/import?locale=en", []byte(`{"a":"b"}`))
	if code, _ := admin.do("POST", cpath+"/import?locale=ja", []byte(`{"hello":"こんにちは"}`), nil); code != 400 {
		t.Fatalf("target import before source must fail: %d", code)
	}
	admin.must(200, "POST", cpath+"/import?locale=en", []byte(`{"hello":"Hello","menu":{"file":"File"}}`))
	admin.must(200, "POST", cpath+"/import?locale=ja", []byte(`{"hello":"こんにちは","menu":{"file":"File"}}`))

	_, raw := trans.do("GET", cpath+"/units?locale=ja", nil, nil)
	var page struct {
		Total int64
		Units []struct {
			ID      int64
			Key     string
			Value   string
			Status  string
			Version int64
		}
	}
	if err := json.Unmarshal(raw, &page); err != nil || len(page.Units) != 2 || page.Total != 2 {
		t.Fatalf("units: %s %v", raw, err)
	}
	units := page.Units
	u := units[1] // /menu/file
	upath := "/api/units/" + itoa(u.ID) + "/translations/ja"
	trans.must(200, "PUT", upath, map[string]any{"value": "ファイル", "status": "translated", "version": u.Version})
	trans.must(409, "PUT", upath, map[string]any{"value": "stale", "status": "translated", "version": u.Version})
	trans.must(403, "PUT", upath, map[string]any{"value": "ファイル", "status": "reviewed", "version": u.Version + 1})
	admin.must(200, "PUT", upath, map[string]any{"value": "ファイル", "status": "reviewed", "version": u.Version + 1})
	trans.must(400, "PUT", "/api/units/"+itoa(u.ID)+"/translations/en", map[string]any{"value": "x", "version": 0})

	_, out := trans.do("GET", cpath+"/export?locale=ja", nil, nil)
	if string(out) != "{\n  \"hello\": \"こんにちは\",\n  \"menu\": {\n    \"file\": \"ファイル\"\n  }\n}\n" {
		t.Fatalf("export: %s", out)
	}

	// Changing the source marks existing translations for review.
	admin.must(200, "POST", cpath+"/import?locale=en", []byte(`{"hello":"Hello!","menu":{"file":"File"}}`))
	_, raw = trans.do("GET", cpath+"/units?locale=ja", nil, nil)
	_ = json.Unmarshal(raw, &page)
	units = page.Units
	if units[0].Status != "needs_review" || units[1].Status != "reviewed" {
		t.Fatalf("statuses after source change: %+v", units)
	}
	// Server-side search, status filter and count.
	_, raw = trans.do("GET", cpath+"/units?locale=ja&q=%25file&status=reviewed", nil, nil)
	_ = json.Unmarshal(raw, &page)
	if page.Total != 0 {
		t.Fatalf("escaped wildcard should not match: %s", raw)
	}
	_, raw = trans.do("GET", cpath+"/units?locale=ja&q=file&status=reviewed", nil, nil)
	_ = json.Unmarshal(raw, &page)
	if page.Total != 1 || len(page.Units) != 1 || page.Units[0].Key != "/menu/file" {
		t.Fatalf("search: %s", raw)
	}
	// History and statistics.
	_, raw = trans.do("GET", "/api/units/"+itoa(u.ID)+"/history?locale=ja", nil, nil)
	var history []struct {
		Value, Status string
		Version       int64
		ChangedByName string `json:"changed_by_name"`
	}
	if err := json.Unmarshal(raw, &history); err != nil || len(history) != 3 || history[0].Status != "reviewed" || history[0].ChangedByName != "Admin" || history[2].Version != 1 {
		t.Fatalf("history: %s %v", raw, err)
	}
	_, raw = trans.do("GET", cpath+"/stats", nil, nil)
	var stats []struct {
		Locale                      string
		Total, Translated, Reviewed int64
		NeedsReview                 int64 `json:"needs_review"`
	}
	_ = json.Unmarshal(raw, &stats)
	if len(stats) != 1 || stats[0].Locale != "ja" || stats[0].Total != 2 || stats[0].Translated != 1 || stats[0].Reviewed != 1 {
		t.Fatalf("stats: %s", raw)
	}
	_, raw = trans.do("GET", ppath+"/history", nil, nil)
	if !strings.Contains(string(raw), `"component_name":"Web"`) {
		t.Fatalf("project history: %s", raw)
	}
	_, raw = trans.do("GET", ppath, nil, nil)
	if !strings.Contains(string(raw), `"role":"translator"`) || !strings.Contains(string(raw), `"code":"ja"`) {
		t.Fatalf("project detail: %s", raw)
	}

	// Glossary and translation memory.
	trans.must(200, "POST", ppath+"/glossary", map[string]string{"locale": "ja", "term": "File", "translation": "ファイル", "note": "menu label"})
	trans.must(400, "POST", ppath+"/glossary", map[string]string{"locale": "en", "term": "File", "translation": "x"})
	trans.must(400, "POST", ppath+"/glossary", map[string]string{"locale": "ja", "term": "", "translation": "x"})
	g := admin.must(200, "POST", ppath+"/glossary", map[string]string{"locale": "ja", "term": "file", "translation": "ファイル（上書き）"})
	_, raw = trans.do("GET", ppath+"/glossary?locale=ja", nil, nil)
	if strings.Count(string(raw), `"term"`) != 1 || !strings.Contains(string(raw), "上書き") {
		t.Fatalf("glossary upsert should be case-insensitive: %s", raw)
	}
	co2 := admin.must(201, "POST", ppath+"/components", map[string]string{"slug": "docs", "name": "Docs", "format": "json"})
	c2path := "/api/components/" + itoa(int64(co2["id"].(float64)))
	admin.must(200, "POST", c2path+"/import?locale=en", []byte(`{"open":"Open file","other":"Something unrelated"}`))
	_, raw = trans.do("GET", c2path+"/units?locale=ja", nil, nil)
	_ = json.Unmarshal(raw, &page)
	var openID int64
	for _, x := range page.Units {
		if x.Key == "/open" {
			openID = x.ID
		}
	}
	_, raw = trans.do("GET", "/api/units/"+itoa(openID)+"/assist?locale=ja", nil, nil)
	var assist struct {
		Memory []struct {
			Source, Value string
			Score         float64
		}
		Glossary []struct{ Term, Translation string }
	}
	if err := json.Unmarshal(raw, &assist); err != nil || len(assist.Glossary) != 1 || assist.Glossary[0].Translation != "ファイル（上書き）" {
		t.Fatalf("assist glossary: %s %v", raw, err)
	}
	if len(assist.Memory) != 1 || assist.Memory[0].Source != "File" || assist.Memory[0].Value != "ファイル" || assist.Memory[0].Score <= 0 {
		t.Fatalf("assist memory: %s", raw)
	}
	// Glossary CSV round trip and autofill from exact memory matches.
	csvIn := "\xef\xbb\xbfterm,translation,note\nEdit,編集,verb\nfile,ファイル,\n,,\n"
	res := trans.must(200, "POST", ppath+"/glossary/import?locale=ja", []byte(csvIn))
	if res["imported"] != float64(2) || res["skipped"] != float64(1) {
		t.Fatalf("csv import: %v", res)
	}
	if code, _ := trans.do("POST", ppath+"/glossary/import", []byte("term,translation\nx,y\n"), nil); code != 400 {
		t.Fatalf("import without locale should be 400: %d", code)
	}
	if code, _ := trans.do("POST", ppath+"/glossary/import", []byte("term,locale,translation\nx,zz-ZZ,y\n"), nil); code != 400 {
		t.Fatalf("unknown locale should be 400: %d", code)
	}
	_, raw = trans.do("GET", ppath+"/glossary/export?locale=ja", nil, nil)
	if !strings.HasPrefix(string(raw), "term,locale,translation,note\nEdit,ja,編集,verb\n") || !strings.Contains(string(raw), "file,ja,ファイル,\n") {
		t.Fatalf("csv export: %s", raw)
	}
	dry := trans.must(200, "POST", c2path+"/autofill", map[string]any{"locale": "ja", "dry_run": true})
	if dry["untranslated"] != float64(2) || dry["matches"] != float64(0) {
		t.Fatalf("dry run before exact match: %v", dry)
	}
	admin.must(200, "POST", c2path+"/import?locale=en", []byte(`{"open":"Open file","other":"Something unrelated","hello":"Hello!"}`))
	dry = trans.must(200, "POST", c2path+"/autofill", map[string]any{"locale": "ja", "dry_run": true})
	if dry["untranslated"] != float64(3) || dry["matches"] != float64(1) || dry["filled"] != float64(0) {
		t.Fatalf("dry run: %v", dry)
	}
	fill := trans.must(200, "POST", c2path+"/autofill", map[string]any{"locale": "ja"})
	if fill["filled"] != float64(1) {
		t.Fatalf("autofill: %v", fill)
	}
	_, raw = trans.do("GET", c2path+"/units?locale=ja&status=needs_review", nil, nil)
	_ = json.Unmarshal(raw, &page)
	if page.Total != 1 || page.Units[0].Key != "/hello" || page.Units[0].Value != "こんにちは" {
		t.Fatalf("autofilled unit: %s", raw)
	}
	trans.must(400, "POST", c2path+"/autofill", map[string]any{"locale": "ja", "status": "reviewed"})
	trans.must(403, "DELETE", ppath+"/glossary/"+itoa(int64(g["id"].(float64))), nil)
	admin.must(204, "DELETE", ppath+"/glossary/"+itoa(int64(g["id"].(float64))), nil)
	admin.must(404, "DELETE", ppath+"/glossary/"+itoa(int64(g["id"].(float64))), nil)

	// Suggestions fail cleanly when providers are unconfigured.
	trans.must(502, "POST", "/api/units/"+itoa(u.ID)+"/suggest", map[string]string{"provider": "openai", "locale": "ja"})
	trans.must(400, "POST", "/api/units/"+itoa(u.ID)+"/suggest", map[string]string{"provider": "nope", "locale": "ja"})

	// Deleting an in-use locale is refused by the foreign key.
	admin.must(400, "DELETE", "/api/locales/ja", nil)
	admin.must(204, "POST", "/api/logout", nil)
	admin.must(401, "GET", "/api/me", nil)
}

func TestWebhook(t *testing.T) {
	_, ts := newTestServer(t)
	admin := login(t, ts.URL, "admin@example.com", "admin-password-123")
	admin.must(200, "POST", "/api/locales", map[string]string{"code": "en", "name": "English"})
	p := admin.must(201, "POST", "/api/projects", map[string]string{"slug": "demo", "name": "Demo", "source_locale": "en"})
	ppath := "/api/projects/" + itoa(int64(p["id"].(float64)))
	r := admin.must(201, "POST", ppath+"/repositories", map[string]string{"url": "https://github.com/owner/repo", "branch": "main"})
	if r["name"] != "owner/repo" || r["url"] != "https://github.com/owner/repo.git" {
		t.Fatalf("repository: %v", r)
	}
	rid := int64(r["id"].(float64))
	admin.must(400, "POST", ppath+"/repositories", map[string]string{"url": "https://gitlab.com/owner/repo.git"})
	admin.must(201, "POST", ppath+"/components", map[string]any{"slug": "web", "name": "Web", "format": "json", "repository_id": rid})
	admin.must(400, "POST", ppath+"/components", map[string]any{"slug": "bad", "name": "Bad", "format": "json", "repository_id": rid + 100})
	st := admin.must(200, "GET", "/api/repositories/"+itoa(rid), nil)
	if st["checkout"].(map[string]any)["exists"] != false {
		t.Fatalf("status: %v", st)
	}
	if code, _ := admin.do("GET", "/api/repositories/"+itoa(rid)+"/scan", nil, nil); code != 409 {
		t.Fatalf("scan before clone should be 409: %d", code)
	}

	body := []byte(`{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/owner/repo.git"}}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	hook := func(headers map[string]string, b []byte) (int, []byte) {
		req, _ := http.NewRequest("POST", ts.URL+"/webhooks/github", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		return res.StatusCode, raw
	}
	if code, _ := hook(map[string]string{"X-Hub-Signature-256": "sha256=00", "X-GitHub-Event": "push", "X-GitHub-Delivery": "d1"}, body); code != 401 {
		t.Fatalf("bad signature: %d", code)
	}
	if code, _ := hook(map[string]string{"X-Hub-Signature-256": sig, "X-GitHub-Event": "ping", "X-GitHub-Delivery": "d0"}, body); code != 204 {
		t.Fatalf("ping: %d", code)
	}
	code, raw := hook(map[string]string{"X-Hub-Signature-256": sig, "X-GitHub-Event": "push", "X-GitHub-Delivery": "d1"}, body)
	if code != 202 || !strings.Contains(string(raw), `"queued":true`) {
		t.Fatalf("push: %d %s", code, raw)
	}
	code, raw = hook(map[string]string{"X-Hub-Signature-256": sig, "X-GitHub-Event": "push", "X-GitHub-Delivery": "d1"}, body)
	if code != 202 || !strings.Contains(string(raw), `"queued":false`) {
		t.Fatalf("duplicate delivery: %d %s", code, raw)
	}
	other := []byte(`{"ref":"refs/heads/dev","repository":{"clone_url":"https://github.com/owner/repo.git"}}`)
	mac = hmac.New(sha256.New, []byte(secret))
	mac.Write(other)
	if code, _ = hook(map[string]string{"X-Hub-Signature-256": "sha256=" + hex.EncodeToString(mac.Sum(nil)), "X-GitHub-Event": "push", "X-GitHub-Delivery": "d2"}, other); code != 204 {
		t.Fatalf("unrelated branch should be ignored: %d", code)
	}
	_, list := admin.do("GET", "/api/deliveries", nil, nil)
	if !strings.Contains(string(list), `"delivery_id":"d1"`) || strings.Contains(string(list), `"d2"`) {
		t.Fatalf("deliveries: %s", list)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
