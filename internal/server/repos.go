package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"
	"konnyaku/internal/db"
	"konnyaku/internal/gitrepo"
	"konnyaku/internal/integrations"
)

func (s *Server) repository(c *echo.Context, min string) (db.Repository, error) {
	rid, err := id(c, "repository")
	if err != nil {
		return db.Repository{}, err
	}
	r, err := s.Q.GetRepository(c.Request().Context(), rid)
	if err != nil {
		return r, err
	}
	return r, s.authorize(c, r.ProjectID, min)
}
func (s *Server) github(r db.Repository) gitrepo.Repository {
	return gitrepo.Repository{URL: r.Url, Branch: r.Branch, Token: s.Config.GitHubToken, HTTP: s.GitHubHTTP}
}

// errRepositoryBusy reports that another operation holds the repository. It is
// an HTTPError so handlers can return it unchanged.
var errRepositoryBusy = echo.NewHTTPError(429, "another repository operation is running, try again")

// gitError reports GitHub API failures as conflicts while leaving
// errRepositoryBusy and other HTTP errors with their own status.
func gitError(err error) error {
	var he *echo.HTTPError
	if err == nil || errors.As(err, &he) || errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var api *gitrepo.APIError
	if errors.As(err, &api) && (api.Status == 429 || api.RetryAfter > 0) {
		return echo.NewHTTPError(429, api.Error())
	}
	return echo.NewHTTPError(409, err.Error())
}

// withRepository serializes GitHub work per repository across processes. Work is
// admitted before a pool connection is taken and the lock is never waited on,
// so queued operations cannot hold the connections the running one still needs.
func (s *Server) withRepository(ctx context.Context, r db.Repository, fn func(context.Context, gitrepo.Repository) error) error {
	if _, err := gitrepo.GitHubRepository(r.Url); err != nil {
		return err
	}
	select {
	case s.repoSlots <- struct{}{}:
		defer func() { <-s.repoSlots }()
	default:
		return errRepositoryBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	// Negative lock IDs reserve a namespace separate from catalog edit locks.
	var locked bool
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", -r.ID).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return errRepositoryBusy
	}
	return fn(ctx, s.github(r))
}
func (s *Server) listRepositories(c *echo.Context) error {
	pid, err := s.projectID(c, "viewer")
	if err != nil {
		return err
	}
	rows, err := s.Q.ListRepositories(c.Request().Context(), pid)
	if err != nil {
		return err
	}
	return c.JSON(200, rows)
}
func (s *Server) createRepository(c *echo.Context) error {
	pid, err := s.projectID(c, "manager")
	if err != nil {
		return err
	}
	var in struct{ Name, URL, Branch string }
	if err = decode(c, &in); err != nil {
		return err
	}
	if in.Branch == "" {
		in.Branch = "main"
	}
	repo, err := gitrepo.GitHubRepository(in.URL)
	if err != nil {
		return echo.NewHTTPError(400, err.Error())
	}
	if err = gitrepo.ValidateBranch(in.Branch); err != nil {
		return echo.NewHTTPError(400, err.Error())
	}
	if in.Name == "" {
		in.Name = repo
	}
	if !validName(in.Name) {
		return echo.NewHTTPError(400, "name too long")
	}
	row, err := s.Q.CreateRepository(c.Request().Context(), db.CreateRepositoryParams{ProjectID: pid, Name: in.Name, Url: "https://github.com/" + repo + ".git", Branch: in.Branch})
	if err != nil {
		return err
	}
	return c.JSON(201, row)
}
func (s *Server) deleteRepository(c *echo.Context) error {
	r, err := s.repository(c, "manager")
	if err != nil {
		return err
	}
	err = s.withRepository(c.Request().Context(), r, func(ctx context.Context, g gitrepo.Repository) error {
		if _, e := s.Q.DeleteRepository(c.Request().Context(), r.ID); e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		return err
	}
	return c.NoContent(204)
}
func (s *Server) repositoryStatus(c *echo.Context) error {
	r, err := s.repository(c, "viewer")
	if err != nil {
		return err
	}
	var commit string
	var syncedAt *time.Time
	err = s.Pool.QueryRow(c.Request().Context(), "SELECT commit_sha,synced_at FROM repository_imports WHERE repository_id=$1 ORDER BY synced_at DESC LIMIT 1", r.ID).Scan(&commit, &syncedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var publication *publication
	var jobID int64
	err = s.Pool.QueryRow(c.Request().Context(), "SELECT id FROM repository_publications WHERE repository_id=$1 ORDER BY id DESC LIMIT 1", r.ID).Scan(&jobID)
	if err == nil && user(c).Admin {
		publication, err = s.loadPublication(c.Request().Context(), r.ID, jobID)
		if publication != nil {
			publication.Changes = nil
		}
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	components, err := s.Q.RepositoryComponents(c.Request().Context(), pgInt8(r.ID))
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"repository": r, "last_import_commit": commit, "synced_at": syncedAt, "publication": publication, "components": components, "github_token": s.Config.GitHubToken != ""})
}
func (s *Server) scanRepository(c *echo.Context) error {
	r, err := s.repository(c, "manager")
	if err != nil {
		return err
	}
	var candidates []gitrepo.Candidate
	err = s.withRepository(c.Request().Context(), r, func(ctx context.Context, g gitrepo.Repository) error {
		snapshot, e := g.Snapshot(ctx)
		if e != nil {
			return e
		}
		candidates, e = snapshot.Scan()
		return e
	})
	if err != nil {
		return gitError(err)
	}
	if candidates == nil {
		candidates = []gitrepo.Candidate{}
	}
	return c.JSON(200, candidates)
}

// SyncResult reports what a repository synchronization did, per file.
type SyncResult struct {
	Files   []SyncFile `json:"files"`
	Ignored []string   `json:"ignored"` // files whose name is not a locale (index.json, ja-KS.yml)
	Errors  []string   `json:"errors"`
}
type SyncFile struct {
	Component string `json:"component"`
	Locale    string `json:"locale"`
	Path      string `json:"path"`
	Unchanged bool   `json:"unchanged,omitempty"`
	ImportResult
}

func (r *SyncResult) failed() bool { return len(r.Errors) > 0 }

// syncRepository imports a single immutable GitHub snapshot, source files first.
func (s *Server) syncRepository(ctx context.Context, r db.Repository, g gitrepo.Repository, uid int64) (*SyncResult, error) {
	res := &SyncResult{Files: []SyncFile{}, Ignored: []string{}, Errors: []string{}}
	snapshot, err := g.Snapshot(ctx)
	if err != nil {
		return res, err
	}

	p, err := s.Q.GetProject(ctx, r.ProjectID)
	if err != nil {
		return res, err
	}
	components, err := s.Q.RepositoryComponents(ctx, pgInt8(r.ID))
	if err != nil {
		return res, err
	}
	if len(components) == 0 {
		res.Errors = append(res.Errors, "no components use this repository yet")
	}
	for _, co := range components {
		files, err := snapshot.LocaleFiles(co.FilePattern)
		if err != nil {
			res.Errors = append(res.Errors, co.Slug+": "+err.Error())
			continue
		}

		src := gitrepo.FindLocaleFile(files, p.SourceLocale)
		if src == nil {
			res.Errors = append(res.Errors, co.Slug+": no source file for "+p.SourceLocale+" matching "+co.FilePattern)
			continue
		}
		sourceSHA := snapshot.Files[src.Path].SHA
		importFile := func(f *gitrepo.LocaleFile, locale string) bool {
			entry := snapshot.Files[f.Path]
			old, e := s.importBaseline(ctx, co, locale)
			if e != nil {
				res.Errors = append(res.Errors, f.Path+": "+errorMessage(e))
				return false
			}
			same := old != nil && old.Path == f.Path && old.Blob == entry.SHA
			if same && old.Source == sourceSHA {
				res.Files = append(res.Files, SyncFile{Component: co.Slug, Locale: locale, Path: f.Path, Unchanged: true})
				return true
			}
			var raw []byte
			if same {
				doc, e := s.Q.GetDocument(ctx, db.GetDocumentParams{ComponentID: co.ID, Locale: locale})
				if e == nil && gitrepo.BlobSHA(doc.Content) == entry.SHA {
					raw = doc.Content
				}
			}
			if raw == nil {
				raw, e = g.Read(ctx, entry)
			}
			var ir ImportResult
			if e == nil {
				ir, e = s.importCatalog(ctx, co, locale, raw, uid, &importOrigin{Path: f.Path, Blob: entry.SHA, Source: sourceSHA, Commit: snapshot.Commit, Preserve: same})
			}
			if e != nil {
				res.Errors = append(res.Errors, f.Path+": "+errorMessage(e))
				return false
			}
			res.Files = append(res.Files, SyncFile{Component: co.Slug, Locale: locale, Path: f.Path, ImportResult: ir})
			return true
		}
		if !importFile(src, p.SourceLocale) {
			continue
		}
		for i := range files {
			f := &files[i]
			if !f.Recognized() {
				res.Ignored = append(res.Ignored, f.Path)
				continue
			}
			if f.Default || f.Path == src.Path || f.Code == p.SourceLocale {
				continue
			}
			_, name, _ := gitrepo.ParseLocaleName(f.Raw)
			if err = s.Q.EnsureLocale(ctx, db.EnsureLocaleParams{Code: f.Code, Name: name}); err != nil {
				return res, err
			}
			importFile(f, f.Code)
		}
	}
	return res, nil
}

// errorMessage unwraps HTTP errors produced by Import into their message.
func errorMessage(err error) string {
	var he *echo.HTTPError
	if errors.As(err, &he) && he.Message != "" {
		return he.Message
	}
	return err.Error()
}

func (s *Server) repositorySync(c *echo.Context) error {
	r, err := s.repository(c, "manager")
	if err != nil {
		return err
	}
	var result *SyncResult
	err = s.withRepository(c.Request().Context(), r, func(ctx context.Context, g gitrepo.Repository) error {
		var e error
		result, e = s.syncRepository(ctx, r, g, user(c).ID)
		return e
	})
	if err != nil {
		return gitError(err)
	}
	return c.JSON(200, map[string]any{"sync": result})
}

// ---- GitHub webhook ------------------------------------------------------------

func (s *Server) webhook(c *echo.Context) error {
	if s.Config.WebhookSecret == "" {
		return echo.NewHTTPError(503, "webhook not configured")
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request().Body, 1<<20+1))
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return echo.NewHTTPError(413, "webhook too large")
	}
	if !integrations.ValidSignature(s.Config.WebhookSecret, c.Request().Header.Get("X-Hub-Signature-256"), raw) {
		return echo.NewHTTPError(401, "invalid signature")
	}
	if c.Request().Header.Get("X-GitHub-Event") != "push" {
		return c.NoContent(204)
	}
	delivery := c.Request().Header.Get("X-GitHub-Delivery")
	if len(delivery) == 0 || len(delivery) > 128 {
		return echo.NewHTTPError(400, "delivery ID required")
	}
	var event struct {
		Ref        string
		Deleted    bool
		Repository struct {
			CloneURL string `json:"clone_url"`
		}
	}
	if err = json.Unmarshal(raw, &event); err != nil {
		return echo.NewHTTPError(400, "invalid webhook")
	}
	if event.Deleted || !strings.HasPrefix(event.Ref, "refs/heads/") {
		return c.NoContent(204)
	}
	if _, err = gitrepo.GitHubRepository(event.Repository.CloneURL); err != nil {
		return echo.NewHTTPError(400, "invalid repository")
	}
	// Only queue explicitly configured repositories; unregistered repositories are never fetched.
	repos, err := s.Q.RepositoriesByURL(c.Request().Context(), event.Repository.CloneURL)
	if err != nil {
		return err
	}
	matched := false
	for _, r := range repos {
		if event.Ref == "refs/heads/"+r.Branch {
			matched = true
			break
		}
	}
	if !matched {
		return c.NoContent(204)
	}
	n, err := s.Q.EnqueueDelivery(c.Request().Context(), db.EnqueueDeliveryParams{DeliveryID: delivery, RepositoryUrl: event.Repository.CloneURL, Ref: event.Ref})
	if err != nil {
		return err
	}
	return c.JSON(202, map[string]any{"queued": n == 1})
}

// RunWorker keeps queue claims locked until processing and status persistence
// finish. A crash rolls the claim back, leaving a pending event for redelivery.
func (s *Server) RunWorker(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.processDelivery(ctx); err != nil && !errors.Is(err, context.Canceled) {
				s.Echo.Logger.Error("webhook worker failed", "error", err)
			}
		}
	}
}
func (s *Server) processDelivery(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	q := s.Q.WithTx(tx)
	event, err := q.ClaimDelivery(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, "SELECT delivery_id FROM webhook_deliveries WHERE repository_url=$1 AND ref=$2 AND status='pending'", event.RepositoryUrl, event.Ref)
	if err != nil {
		return err
	}
	var coalesced []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		coalesced = append(coalesced, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	repos, err := s.Q.RepositoriesByURL(ctx, event.RepositoryUrl)
	if err != nil {
		return err
	}
	status, message := "done", ""
	for _, r := range repos {
		if "refs/heads/"+r.Branch != event.Ref {
			continue
		}
		err = s.withRepository(ctx, r, func(ctx context.Context, g gitrepo.Repository) error {
			sync, e := s.syncRepository(ctx, r, g, 0)
			if e == nil && sync.failed() {
				e = errors.New(strings.Join(sync.Errors, "; "))
			}
			return e
		})
		if errors.Is(err, errRepositoryBusy) {
			// Roll the claim back so the next tick redelivers the event
			// instead of recording a failure that needs a manual retry.
			return nil
		}
		if err != nil {
			status = "failed"
			message = "repository synchronization failed: " + err.Error()
			if len(message) > 500 {
				message = message[:500]
			}
			s.Echo.Logger.Error("repository synchronization failed", "repository", r.ID, "error", err)
			break
		}
	}
	if err = q.FinishDelivery(ctx, db.FinishDeliveryParams{DeliveryID: event.DeliveryID, Status: status, Error: message}); err != nil {
		return err
	}
	if status == "done" && len(coalesced) > 0 {
		if _, err = tx.Exec(ctx, "UPDATE webhook_deliveries SET status='done',error='' WHERE delivery_id=ANY($1::text[]) AND status='pending'", coalesced); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
