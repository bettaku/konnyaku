package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
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
func (s *Server) checkout(r db.Repository) gitrepo.Repository {
	return gitrepo.Repository{Root: s.Config.RepositoryRoot, ID: r.ID, URL: r.Url, Branch: r.Branch, Token: s.Config.GitHubToken}
}

// withRepository serializes Git work per repository across processes.
func (s *Server) withRepository(ctx context.Context, r db.Repository, fn func(gitrepo.Repository) error) error {
	if _, err := gitrepo.GitHubRepository(r.Url); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	// Negative lock IDs reserve a namespace separate from catalog edit locks.
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", -r.ID); err != nil {
		return err
	}
	return fn(s.checkout(r))
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
	err = s.withRepository(c.Request().Context(), r, func(g gitrepo.Repository) error {
		if _, e := s.Q.DeleteRepository(c.Request().Context(), r.ID); e != nil {
			return e
		}
		return os.RemoveAll(g.Dir())
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
	st, err := s.checkout(r).Status(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(409, err.Error())
	}
	components, err := s.Q.RepositoryComponents(c.Request().Context(), pgInt8(r.ID))
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"repository": r, "checkout": st, "components": components, "github_token": s.Config.GitHubToken != ""})
}
func (s *Server) scanRepository(c *echo.Context) error {
	r, err := s.repository(c, "manager")
	if err != nil {
		return err
	}
	var candidates []gitrepo.Candidate
	err = s.withRepository(c.Request().Context(), r, func(g gitrepo.Repository) error {
		if _, e := os.Lstat(g.Dir()); errors.Is(e, os.ErrNotExist) {
			return errors.New("clone the repository first")
		}
		var e error
		candidates, e = g.Scan()
		return e
	})
	if err != nil {
		return echo.NewHTTPError(409, err.Error())
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
	ImportResult
}

func (r *SyncResult) failed() bool { return len(r.Errors) > 0 }

// syncRepository imports every locale file the checkout holds for each attached
// component: the source catalog first (falling back to a language-only or
// default file), then all other locale files. Locales found in the repository
// are registered automatically so a freshly cloned project starts with its
// existing translations instead of at 0%. Problems are collected per file and
// never stop the remaining files from being imported.
func (s *Server) syncRepository(ctx context.Context, r db.Repository, g gitrepo.Repository, uid int64) (*SyncResult, error) {
	res := &SyncResult{Files: []SyncFile{}, Ignored: []string{}, Errors: []string{}}
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
		files, err := g.LocaleFiles(co.FilePattern)
		if err != nil {
			res.Errors = append(res.Errors, co.Slug+": "+err.Error())
			continue
		}
		importFile := func(f *gitrepo.LocaleFile, locale string) bool {
			raw, err := g.Read(f.Path)
			var ir ImportResult
			if err == nil {
				ir, err = s.Import(ctx, co, locale, raw, uid)
			}
			if err != nil {
				res.Errors = append(res.Errors, f.Path+": "+errorMessage(err))
				return false
			}
			res.Files = append(res.Files, SyncFile{Component: co.Slug, Locale: locale, Path: f.Path, ImportResult: ir})
			return true
		}
		src := gitrepo.FindLocaleFile(files, p.SourceLocale)
		if src == nil {
			res.Errors = append(res.Errors, co.Slug+": no source file for "+p.SourceLocale+" matching "+co.FilePattern)
			continue
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

// exportRepository writes every target locale of every attached component into
// the checkout, reusing the spelling of existing files, and returns the paths.
func (s *Server) exportRepository(ctx context.Context, r db.Repository, g gitrepo.Repository) ([]string, error) {
	targets, err := s.Q.ProjectLocales(ctx, r.ProjectID)
	if err != nil {
		return nil, err
	}
	components, err := s.Q.RepositoryComponents(ctx, pgInt8(r.ID))
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, co := range components {
		files, err := g.LocaleFiles(co.FilePattern)
		if err != nil {
			return nil, errors.New(co.Slug + ": " + err.Error())
		}
		for _, t := range targets {
			raw, err := s.Export(ctx, co, t.Code)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, errors.New(co.Slug + " (" + t.Code + "): " + err.Error())
			}
			path := gitrepo.LocalePath(co.FilePattern, t.Code, files)
			if f := gitrepo.FindLocaleFile(files, t.Code); f != nil && !f.Default {
				path = f.Path
			}
			if err = g.Write(path, raw); err != nil {
				return nil, err
			}
			paths = append(paths, path)
		}
	}
	return paths, nil
}
func (s *Server) repositoryAction(c *echo.Context) error {
	r, err := s.repository(c, "manager")
	if err != nil {
		return err
	}
	if !user(c).Admin && c.Param("action") != "sync" {
		return echo.NewHTTPError(403, "administrator required for remote Git operations")
	}
	ctx := c.Request().Context()
	var in struct{ Message string }
	if err = decode(c, &in); err != nil {
		return err
	}
	result := map[string]any{}
	err = s.withRepository(ctx, r, func(g gitrepo.Repository) error {
		switch c.Param("action") {
		case "clone":
			return g.Clone(ctx)
		case "pull":
			return g.Pull(ctx)
		case "push":
			return g.Push(ctx)
		case "sync":
			sync, e := s.syncRepository(ctx, r, g, user(c).ID)
			result["sync"] = sync
			return e
		case "commit":
			if e := g.Clean(ctx); e != nil {
				return e
			}
			if e := g.Checkout(ctx, r.Branch, false); e != nil {
				return e
			}
			paths, e := s.exportRepository(ctx, r, g)
			if e != nil {
				return e
			}
			if in.Message == "" {
				in.Message = "Update translations"
			}
			e = g.CommitPaths(ctx, paths, in.Message)
			if errors.Is(e, gitrepo.ErrNothingToCommit) {
				result["committed"] = false
				return nil
			}
			result["committed"] = true
			return e
		default:
			return echo.NewHTTPError(400, "unknown repository action")
		}
	})
	var he *echo.HTTPError
	if errors.As(err, &he) {
		return err
	}
	if err != nil {
		return echo.NewHTTPError(409, err.Error())
	}
	result["status"] = "done"
	return c.JSON(200, result)
}

// repositoryPullRequest exports translations on a fresh branch, pushes it and
// opens a draft pull request against the tracked branch.
func (s *Server) repositoryPullRequest(c *echo.Context) error {
	r, err := s.repository(c, "manager")
	if err != nil {
		return err
	}
	if !user(c).Admin {
		return echo.NewHTTPError(403, "administrator required")
	}
	repo, err := gitrepo.GitHubRepository(r.Url)
	if err != nil {
		return echo.NewHTTPError(400, err.Error())
	}
	var in struct{ Title, Body string }
	if err = decode(c, &in); err != nil {
		return err
	}
	if in.Title == "" {
		in.Title = "Update translations"
	}
	if !validName(in.Title) || len(in.Body) > 10000 {
		return echo.NewHTTPError(400, "invalid title or body")
	}
	ctx := c.Request().Context()
	branch := "konnyaku/translations-" + time.Now().UTC().Format("20060102-150405")
	url := ""
	err = s.withRepository(ctx, r, func(g gitrepo.Repository) error {
		if e := g.Pull(ctx); e != nil {
			return e
		}
		if e := g.Checkout(ctx, branch, true); e != nil {
			return e
		}
		// Always return to the tracked branch so later syncs read the right files.
		defer func() { _ = g.Checkout(context.Background(), r.Branch, false) }()
		paths, e := s.exportRepository(ctx, r, g)
		if e != nil {
			return e
		}
		if e = g.CommitPaths(ctx, paths, in.Title); e != nil {
			return e
		}
		if e = g.PushBranch(ctx, branch); e != nil {
			return e
		}
		url, e = integrations.CreatePR(ctx, nil, s.Config.GitHubToken, repo, integrations.PullRequest{Title: in.Title, Head: branch, Base: r.Branch, Body: in.Body})
		return e
	})
	if errors.Is(err, gitrepo.ErrNothingToCommit) {
		return echo.NewHTTPError(409, "no translation changes to propose")
	}
	if err != nil {
		return echo.NewHTTPError(409, err.Error())
	}
	return c.JSON(201, map[string]string{"url": url, "branch": branch})
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
	// Only queue explicitly configured repositories; URLs from events never create checkouts on their own.
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
	repos, err := s.Q.RepositoriesByURL(ctx, event.RepositoryUrl)
	if err != nil {
		return err
	}
	status, message := "done", ""
	for _, r := range repos {
		if "refs/heads/"+r.Branch != event.Ref {
			continue
		}
		err = s.withRepository(ctx, r, func(g gitrepo.Repository) error {
			if _, e := os.Lstat(g.Dir()); errors.Is(e, os.ErrNotExist) {
				if e = g.Clone(ctx); e != nil {
					return e
				}
			} else if e = g.Pull(ctx); e != nil {
				return e
			}
			sync, e := s.syncRepository(ctx, r, g, 0)
			if e == nil && sync.failed() {
				e = errors.New(strings.Join(sync.Errors, "; "))
			}
			return e
		})
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
	return tx.Commit(ctx)
}
