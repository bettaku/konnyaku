package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"
	"konnyaku/internal/db"
	"konnyaku/internal/formats"
	"konnyaku/internal/gitrepo"
)

type importOrigin struct {
	Path, Blob, Source, Commit string
	Preserve                   bool
}

func (s *Server) importBaseline(ctx context.Context, co db.Component, locale string) (*importOrigin, error) {
	return baseline(ctx, s.Pool, co, locale)
}
func baseline(ctx context.Context, q db.DBTX, co db.Component, locale string) (*importOrigin, error) {
	var o importOrigin
	err := q.QueryRow(ctx, `SELECT path,blob_sha,source_sha,commit_sha FROM repository_imports
  WHERE component_id=$1 AND locale=$2 AND repository_id=$3 AND file_pattern=$4 AND format=$5`, co.ID, locale, co.RepositoryID, co.FilePattern, co.Format).Scan(&o.Path, &o.Blob, &o.Source, &o.Commit)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &o, err
}

type publication struct {
	ID        int64            `json:"id"`
	Branch    string           `json:"branch"`
	Base      string           `json:"base"`
	Tree      string           `json:"-"`
	Title     string           `json:"title"`
	Body      string           `json:"body"`
	Changes   []gitrepo.Change `json:"changes,omitempty"`
	Commit    string           `json:"commit"`
	URL       string           `json:"url"`
	CreatedAt time.Time        `json:"created_at"`
}

func (s *Server) loadPublication(ctx context.Context, rid, id int64) (*publication, error) {
	p := &publication{}
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT id,branch,base_sha,tree_sha,title,body,changes,commit_sha,url,created_at
  FROM repository_publications WHERE repository_id=$1 AND id=$2`, rid, id).Scan(&p.ID, &p.Branch, &p.Base, &p.Tree, &p.Title, &p.Body, &raw, &p.Commit, &p.URL, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return p, json.Unmarshal(raw, &p.Changes)
}

// exportRepository freezes a consistent database snapshot. GitHub provenance
// must match the source and each existing target before any remote writes occur.
func (s *Server) exportRepository(ctx context.Context, r db.Repository, g gitrepo.Repository, snapshot gitrepo.Snapshot) ([]gitrepo.Change, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	reader := &Server{Q: s.Q.WithTx(tx)}
	project, err := reader.Q.GetProject(ctx, r.ProjectID)
	if err != nil {
		return nil, err
	}
	targets, err := reader.Q.ProjectLocales(ctx, r.ProjectID)
	if err != nil {
		return nil, err
	}
	components, err := reader.Q.RepositoryComponents(ctx, pgInt8(r.ID))
	if err != nil {
		return nil, err
	}
	changes := []gitrepo.Change{}
	paths := map[string]bool{}
	sources := map[string]bool{}
	total := 0
	for _, co := range components {
		files, err := snapshot.LocaleFiles(co.FilePattern)
		if err != nil {
			return nil, err
		}
		source := gitrepo.FindLocaleFile(files, project.SourceLocale)
		origin, err := baseline(ctx, tx, co, project.SourceLocale)
		if err != nil {
			return nil, err
		}
		if source == nil || origin == nil || origin.Path != source.Path || origin.Blob != snapshot.Files[source.Path].SHA {
			return nil, fmt.Errorf("%s: source changed or was never synchronized; sync from GitHub first", co.Slug)
		}
		sources[source.Path] = true
		for _, locale := range targets {
			if locale.Code == project.SourceLocale {
				continue
			}
			path := gitrepo.LocalePath(co.FilePattern, locale.Code, files)
			if f := gitrepo.FindLocaleFile(files, locale.Code); f != nil && !f.Default {
				path = f.Path
			}
			// Language aliases must never cause a target export to replace the source.
			if path == source.Path {
				continue
			}
			if paths[path] {
				return nil, fmt.Errorf("multiple components or locales export to %s", path)
			}
			paths[path] = true
			old, err := baseline(ctx, tx, co, locale.Code)
			if err != nil {
				return nil, err
			}
			entry, exists := snapshot.Files[path]
			if exists && (!entry.Regular() || old == nil || old.Path != path || old.Blob != entry.SHA || old.Source != origin.Blob) {
				return nil, fmt.Errorf("%s: remote file changed or was never synchronized; sync from GitHub first", path)
			}
			if !exists && old != nil {
				return nil, fmt.Errorf("%s: remote file was removed; resolve the file pattern before publishing", path)
			}
			// Reject writes beneath a symlink, submodule or file, including paths for
			// newly added locales. Tree entries themselves are safe ancestors.
			for parent := path; strings.Contains(parent, "/"); {
				parent = parent[:strings.LastIndex(parent, "/")]
				if e, ok := snapshot.Files[parent]; ok && e.Type != "tree" {
					return nil, fmt.Errorf("unsafe parent of %s", path)
				}
			}
			raw, err := reader.Export(ctx, co, locale.Code)
			if err != nil {
				return nil, err
			}
			if len(raw) > formats.MaxSize || !utf8.Valid(raw) {
				return nil, fmt.Errorf("%s: export must be UTF-8 and at most 4 MiB", path)
			}
			if exists && gitrepo.BlobSHA(raw) == entry.SHA {
				continue
			}
			var before []byte
			mode := "100644"
			if exists {
				mode = entry.Mode
				doc, e := reader.Q.GetDocument(ctx, db.GetDocumentParams{ComponentID: co.ID, Locale: locale.Code})
				if e == nil && gitrepo.BlobSHA(doc.Content) == entry.SHA {
					before = doc.Content
				} else {
					before, err = g.Read(ctx, entry)
					if err != nil {
						return nil, err
					}
				}
			}
			total += len(raw) + len(before)
			if total > 16<<20 {
				return nil, errors.New("publication exceeds 16 MiB; split the components across separate repository registrations")
			}
			changes = append(changes, gitrepo.Change{Path: path, Mode: mode, Before: string(before), Content: string(raw)})
		}
	}
	for _, change := range changes {
		if sources[change.Path] {
			return nil, fmt.Errorf("%s: target overlaps another component's source file", change.Path)
		}
		for parent := change.Path; strings.Contains(parent, "/"); {
			parent = parent[:strings.LastIndex(parent, "/")]
			if paths[parent] {
				return nil, fmt.Errorf("conflicting export paths: %s and %s", parent, change.Path)
			}
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

func (s *Server) repositoryPreview(c *echo.Context) error {
	r, err := s.repository(c, "manager")
	if err != nil {
		return err
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
	var result *publication
	err = s.withRepository(c.Request().Context(), r, func(ctx context.Context, g gitrepo.Repository) error {
		snapshot, e := g.Snapshot(ctx)
		if e != nil {
			return e
		}
		changes, e := s.exportRepository(ctx, r, g, snapshot)
		if e != nil {
			return e
		}
		if len(changes) == 0 {
			return echo.NewHTTPError(409, "no translation changes to propose")
		}
		raw, e := json.Marshal(changes)
		if e != nil {
			return e
		}
		digest, _ := json.Marshal([]any{snapshot.Commit, in.Title, in.Body, changes})
		sum := sha256.Sum256(digest)
		fingerprint := hex.EncodeToString(sum[:])
		branch := fmt.Sprintf("konnyaku/translations-%d-%s", r.ID, fingerprint[:24])
		var id int64
		e = s.Pool.QueryRow(ctx, `INSERT INTO repository_publications(repository_id,fingerprint,branch,base_sha,tree_sha,title,body,changes)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(repository_id,fingerprint) DO UPDATE SET fingerprint=excluded.fingerprint RETURNING id`,
			r.ID, fingerprint, branch, snapshot.Commit, snapshot.Tree, in.Title, in.Body, raw).Scan(&id)
		if e != nil {
			return e
		}
		// Expire only previews that never started publishing. In-progress jobs must
		// retain their payload and stable identity for recovery after a restart.
		if _, e = s.Pool.Exec(ctx, `DELETE FROM repository_publications WHERE repository_id=$1 AND commit_sha='' AND created_at<now()-interval '7 days' AND id<>$2`, r.ID, id); e != nil {
			return e
		}
		result, e = s.loadPublication(ctx, r.ID, id)
		return e
	})
	if err != nil {
		return gitError(err)
	}
	return c.JSON(200, result)
}
func (s *Server) repositoryPublication(c *echo.Context) error {
	r, err := s.repository(c, "manager")
	if err != nil {
		return err
	}
	job, err := id(c, "publication")
	if err != nil {
		return err
	}
	p, err := s.loadPublication(c.Request().Context(), r.ID, job)
	if err != nil {
		return err
	}
	return c.JSON(200, p)
}
func (s *Server) repositoryPullRequest(c *echo.Context) error {
	r, err := s.repository(c, "manager")
	if err != nil {
		return err
	}
	if s.Config.GitHubToken == "" {
		return echo.NewHTTPError(503, "GITHUB_TOKEN not configured")
	}
	var in struct {
		PublicationID int64 `json:"publication_id"`
	}
	if err = decode(c, &in); err != nil {
		return err
	}
	if in.PublicationID <= 0 {
		return echo.NewHTTPError(400, "preview the changes before publishing")
	}
	var result *publication
	err = s.withRepository(c.Request().Context(), r, func(ctx context.Context, g gitrepo.Repository) error {
		p, e := s.loadPublication(ctx, r.ID, in.PublicationID)
		if e != nil {
			return e
		}
		result = p
		if p.URL != "" {
			return nil
		}
		// Reconcile a lost PR response before checking the base: the base may have
		// moved after a successful publication (including merging that very PR).
		existing := ""
		if p.Commit != "" {
			existing, e = g.FindPR(ctx, p.Branch, p.Commit)
			if e != nil {
				return e
			}
		}
		if existing != "" {
			p.URL = existing
		} else {
			head, e := g.Head(ctx, r.Branch)
			if e != nil {
				return e
			}
			if head != p.Base {
				return echo.NewHTTPError(409, "GitHub branch changed since preview; review a new preview")
			}
			if p.Commit == "" {
				p.Commit, e = g.CreateCommit(ctx, gitrepo.Snapshot{Commit: p.Base, Tree: p.Tree}, p.Changes, p.Title, p.CreatedAt)
				if e != nil {
					return e
				}
				if _, e = s.Pool.Exec(ctx, "UPDATE repository_publications SET commit_sha=$1 WHERE id=$2", p.Commit, p.ID); e != nil {
					return e
				}
			}
			if e = g.EnsureBranch(ctx, p.Branch, p.Commit); e != nil {
				return e
			}
			p.URL, e = g.EnsurePR(ctx, p.Branch, p.Commit, p.Title, p.Body)
			if e != nil {
				return e
			}
		}
		// The remote PR now owns the reviewable diff. Release the temporary copies.
		if _, e = s.Pool.Exec(ctx, "UPDATE repository_publications SET url=$1,changes='[]'::jsonb WHERE id=$2", p.URL, p.ID); e != nil {
			return e
		}
		p.Changes = nil
		return nil
	})
	if err != nil {
		return gitError(err)
	}
	return c.JSON(201, result)
}
