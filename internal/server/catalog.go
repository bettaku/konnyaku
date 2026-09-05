package server

import (
	"context"
	"errors"
	"io"
	"net/mail"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/text/language"
	"konnyaku/internal/db"
	"konnyaku/internal/formats"
	"konnyaku/internal/gitrepo"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func validName(v string) bool { return strings.TrimSpace(v) != "" && len(v) <= 200 }
func localeCode(v string) (string, error) {
	tag, err := language.Parse(v)
	if err != nil || v == "" || len(v) > 64 {
		return "", echo.NewHTTPError(400, "invalid locale")
	}
	return tag.String(), nil
}

func CreateUser(ctx context.Context, q *db.Queries, email, password, name string, admin bool) (db.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 || !validName(name) || len(password) < 12 || len(password) > 72 {
		return db.User{}, echo.NewHTTPError(400, "valid email, name and 12–72 byte password required")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return db.User{}, err
	}
	return q.CreateUser(ctx, db.CreateUserParams{Email: email, PasswordHash: string(h), Name: name, Admin: admin})
}
func (s *Server) listUsers(c *echo.Context) error {
	rows, err := s.Q.ListUsers(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(200, rows)
}
func (s *Server) createUser(c *echo.Context) error {
	var in struct {
		Email, Password, Name string
		Admin                 bool
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	u, err := CreateUser(c.Request().Context(), s.Q, in.Email, in.Password, in.Name, in.Admin)
	if err != nil {
		return err
	}
	return c.JSON(201, map[string]any{"id": u.ID, "email": u.Email, "name": u.Name, "admin": u.Admin})
}
func (s *Server) listLocales(c *echo.Context) error {
	rows, err := s.Q.ListLocales(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(200, rows)
}
func (s *Server) saveLocale(c *echo.Context) error {
	var in struct{ Code, Name string }
	if err := decode(c, &in); err != nil {
		return err
	}
	code, err := localeCode(in.Code)
	if err != nil {
		return err
	}
	if !validName(in.Name) {
		return echo.NewHTTPError(400, "name required")
	}
	row, err := s.Q.SaveLocale(c.Request().Context(), db.SaveLocaleParams{Code: code, Name: in.Name})
	if err != nil {
		return err
	}
	return c.JSON(200, row)
}
func (s *Server) deleteLocale(c *echo.Context) error {
	_, err := s.Q.DeleteLocale(c.Request().Context(), c.Param("code"))
	if err != nil {
		return err
	}
	return c.NoContent(204)
}

// ---- projects ---------------------------------------------------------------

func (s *Server) listProjects(c *echo.Context) error {
	u := user(c)
	rows, err := s.Q.ListProjects(c.Request().Context(), db.ListProjectsParams{IsAdmin: u.Admin, UserID: u.ID})
	if err != nil {
		return err
	}
	return c.JSON(200, rows)
}
func (s *Server) createProject(c *echo.Context) error {
	var in struct {
		Slug, Name   string
		SourceLocale string `json:"source_locale"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if !slugPattern.MatchString(in.Slug) || !validName(in.Name) {
		return echo.NewHTTPError(400, "valid slug and name required")
	}
	row, err := s.Q.CreateProject(c.Request().Context(), db.CreateProjectParams{Slug: in.Slug, Name: in.Name, SourceLocale: in.SourceLocale})
	if err != nil {
		return err
	}
	return c.JSON(201, row)
}
func (s *Server) projectID(c *echo.Context, min string) (int64, error) {
	pid, err := id(c, "project")
	if err != nil {
		return 0, err
	}
	return pid, s.authorize(c, pid, min)
}

// getProject returns the project together with the caller's effective role.
func (s *Server) getProject(c *echo.Context) error {
	pid, err := s.projectID(c, "viewer")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	p, err := s.Q.GetProject(ctx, pid)
	if err != nil {
		return err
	}
	role := "admin"
	if !user(c).Admin {
		if role, err = s.Q.GetRole(ctx, db.GetRoleParams{ProjectID: pid, UserID: user(c).ID}); err != nil {
			return err
		}
	}
	locales, err := s.Q.ProjectLocales(ctx, pid)
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"project": p, "role": role, "locales": locales})
}
func (s *Server) renameProject(c *echo.Context) error {
	pid, err := s.projectID(c, "manager")
	if err != nil {
		return err
	}
	var in struct{ Name string }
	if err = decode(c, &in); err != nil {
		return err
	}
	if !validName(in.Name) {
		return echo.NewHTTPError(400, "name required")
	}
	row, err := s.Q.RenameProject(c.Request().Context(), db.RenameProjectParams{ID: pid, Name: in.Name})
	if err != nil {
		return err
	}
	return c.JSON(200, row)
}
func (s *Server) deleteProject(c *echo.Context) error {
	pid, err := s.projectID(c, "manager")
	if err != nil {
		return err
	}
	_, err = s.Q.DeleteProject(c.Request().Context(), pid)
	if err != nil {
		return err
	}
	return c.NoContent(204)
}
func (s *Server) projectLocales(c *echo.Context) error {
	pid, err := s.projectID(c, "viewer")
	if err != nil {
		return err
	}
	rows, err := s.Q.ProjectLocales(c.Request().Context(), pid)
	if err != nil {
		return err
	}
	return c.JSON(200, rows)
}
func (s *Server) addProjectLocale(c *echo.Context) error {
	pid, err := s.projectID(c, "manager")
	if err != nil {
		return err
	}
	loc, err := localeCode(c.Param("locale"))
	if err != nil {
		return err
	}
	p, err := s.Q.GetProject(c.Request().Context(), pid)
	if err != nil {
		return err
	}
	if loc == p.SourceLocale {
		return echo.NewHTTPError(400, "source locale is implicit")
	}
	if err = s.Q.AddProjectLocale(c.Request().Context(), db.AddProjectLocaleParams{ProjectID: pid, Locale: loc}); err != nil {
		return err
	}
	return c.NoContent(204)
}
func (s *Server) removeProjectLocale(c *echo.Context) error {
	pid, err := s.projectID(c, "manager")
	if err != nil {
		return err
	}
	if err = s.Q.RemoveProjectLocale(c.Request().Context(), db.RemoveProjectLocaleParams{ProjectID: pid, Locale: c.Param("locale")}); err != nil {
		return err
	}
	return c.NoContent(204)
}
func (s *Server) projectStats(c *echo.Context) error {
	pid, err := s.projectID(c, "viewer")
	if err != nil {
		return err
	}
	rows, err := s.Q.ProjectStats(c.Request().Context(), pid)
	if err != nil {
		return err
	}
	return c.JSON(200, rows)
}
func (s *Server) projectHistory(c *echo.Context) error {
	pid, err := s.projectID(c, "viewer")
	if err != nil {
		return err
	}
	rows, err := s.Q.ProjectHistory(c.Request().Context(), pid)
	if err != nil {
		return err
	}
	return c.JSON(200, rows)
}
func (s *Server) listMembers(c *echo.Context) error {
	pid, err := s.projectID(c, "manager")
	if err != nil {
		return err
	}
	rows, err := s.Q.ListMembers(c.Request().Context(), pid)
	if err != nil {
		return err
	}
	return c.JSON(200, rows)
}
func (s *Server) saveMember(c *echo.Context) error {
	pid, err := s.projectID(c, "manager")
	if err != nil {
		return err
	}
	uid, err := id(c, "member")
	if err != nil {
		return err
	}
	var in struct{ Role string }
	if err = decode(c, &in); err != nil {
		return err
	}
	if in.Role != "viewer" && in.Role != "translator" && in.Role != "manager" {
		return echo.NewHTTPError(400, "invalid role")
	}
	if err = s.Q.SaveMember(c.Request().Context(), db.SaveMemberParams{ProjectID: pid, UserID: uid, Role: in.Role}); err != nil {
		return err
	}
	return c.NoContent(204)
}
func (s *Server) deleteMember(c *echo.Context) error {
	pid, err := s.projectID(c, "manager")
	if err != nil {
		return err
	}
	uid, err := id(c, "member")
	if err != nil {
		return err
	}
	if err = s.Q.DeleteMember(c.Request().Context(), db.DeleteMemberParams{ProjectID: pid, UserID: uid}); err != nil {
		return err
	}
	return c.NoContent(204)
}

// ---- components -------------------------------------------------------------

func (s *Server) listComponents(c *echo.Context) error {
	pid, err := s.projectID(c, "viewer")
	if err != nil {
		return err
	}
	rows, err := s.Q.ListComponents(c.Request().Context(), pid)
	if err != nil {
		return err
	}
	return c.JSON(200, rows)
}

type componentInput struct {
	Slug, Name, Format string
	RepositoryID       *int64 `json:"repository_id"`
	FilePattern        string `json:"file_pattern"`
}

// resolveRepository validates a repository reference against the project.
func (s *Server) resolveRepository(ctx context.Context, pid int64, rid *int64) (pgtype.Int8, error) {
	if rid == nil || *rid == 0 {
		return pgtype.Int8{}, nil
	}
	r, err := s.Q.GetRepository(ctx, *rid)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && r.ProjectID != pid) {
		return pgtype.Int8{}, echo.NewHTTPError(400, "repository does not belong to this project")
	}
	if err != nil {
		return pgtype.Int8{}, err
	}
	return pgtype.Int8{Int64: r.ID, Valid: true}, nil
}
func (s *Server) createComponent(c *echo.Context) error {
	pid, err := s.projectID(c, "manager")
	if err != nil {
		return err
	}
	var in componentInput
	if err = decode(c, &in); err != nil {
		return err
	}
	if !slugPattern.MatchString(in.Slug) || !validName(in.Name) {
		return echo.NewHTTPError(400, "valid slug and name required")
	}
	if in.Format != "json" && in.Format != "yaml" && in.Format != "po" && in.Format != "android" {
		return echo.NewHTTPError(400, "invalid format")
	}
	if in.FilePattern == "" {
		ext := in.Format
		if ext == "android" {
			ext = "xml"
		}
		in.FilePattern = "locales/{locale}." + ext
	}
	if err = gitrepo.ValidatePattern(in.FilePattern); err != nil {
		return echo.NewHTTPError(400, err.Error())
	}
	rid, err := s.resolveRepository(c.Request().Context(), pid, in.RepositoryID)
	if err != nil {
		return err
	}
	row, err := s.Q.CreateComponent(c.Request().Context(), db.CreateComponentParams{ProjectID: pid, Slug: in.Slug, Name: in.Name, Format: in.Format, RepositoryID: rid, FilePattern: in.FilePattern})
	if err != nil {
		return err
	}
	return c.JSON(201, row)
}
func (s *Server) updateComponent(c *echo.Context) error {
	co, err := s.component(c, "manager")
	if err != nil {
		return err
	}
	var in struct {
		Name         string
		RepositoryID *int64 `json:"repository_id"`
		FilePattern  string `json:"file_pattern"`
	}
	if err = decode(c, &in); err != nil {
		return err
	}
	if in.Name == "" {
		in.Name = co.Name
	}
	if in.FilePattern == "" {
		in.FilePattern = co.FilePattern
	}
	rid := co.RepositoryID
	if in.RepositoryID != nil {
		if rid, err = s.resolveRepository(c.Request().Context(), co.ProjectID, in.RepositoryID); err != nil {
			return err
		}
	}
	if !validName(in.Name) {
		return echo.NewHTTPError(400, "name required")
	}
	if err = gitrepo.ValidatePattern(in.FilePattern); err != nil {
		return echo.NewHTTPError(400, err.Error())
	}
	row, err := s.Q.UpdateComponent(c.Request().Context(), db.UpdateComponentParams{ID: co.ID, Name: in.Name, RepositoryID: rid, FilePattern: in.FilePattern})
	if err != nil {
		return err
	}
	return c.JSON(200, row)
}
func (s *Server) deleteComponent(c *echo.Context) error {
	co, err := s.component(c, "manager")
	if err != nil {
		return err
	}
	_, err = s.Q.DeleteComponent(c.Request().Context(), co.ID)
	if err != nil {
		return err
	}
	return c.NoContent(204)
}

// getComponent returns the component, its project, target locales and the caller's role.
func (s *Server) getComponent(c *echo.Context) error {
	co, err := s.component(c, "viewer")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	p, err := s.Q.GetProject(ctx, co.ProjectID)
	if err != nil {
		return err
	}
	role := "admin"
	if !user(c).Admin {
		if role, err = s.Q.GetRole(ctx, db.GetRoleParams{ProjectID: co.ProjectID, UserID: user(c).ID}); err != nil {
			return err
		}
	}
	locales, err := s.Q.ProjectLocales(ctx, co.ProjectID)
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"component": co, "project": p, "role": role, "locales": locales})
}
func (s *Server) componentStats(c *echo.Context) error {
	co, err := s.component(c, "viewer")
	if err != nil {
		return err
	}
	rows, err := s.Q.ComponentStats(c.Request().Context(), co.ID)
	if err != nil {
		return err
	}
	return c.JSON(200, rows)
}
func (s *Server) componentHistory(c *echo.Context) error {
	co, err := s.component(c, "viewer")
	if err != nil {
		return err
	}
	loc := ""
	if v := c.QueryParam("locale"); v != "" {
		if loc, err = localeCode(v); err != nil {
			return err
		}
	}
	rows, err := s.Q.ComponentHistory(c.Request().Context(), db.ComponentHistoryParams{ComponentID: co.ID, Locale: loc})
	if err != nil {
		return err
	}
	return c.JSON(200, rows)
}
func likePattern(q string) string {
	if q == "" {
		return ""
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}
func (s *Server) listUnits(c *echo.Context) error {
	co, err := s.component(c, "viewer")
	if err != nil {
		return err
	}
	locale, err := localeCode(c.QueryParam("locale"))
	if err != nil {
		return err
	}
	offset := int64(0)
	if v := c.QueryParam("offset"); v != "" {
		offset, err = strconv.ParseInt(v, 10, 32)
		if err != nil || offset < 0 {
			return echo.NewHTTPError(400, "invalid offset")
		}
	}
	status := c.QueryParam("status")
	switch status {
	case "", "untranslated", "translated", "reviewed", "needs_review":
	default:
		return echo.NewHTTPError(400, "invalid status")
	}
	q := strings.TrimSpace(c.QueryParam("q"))
	if len(q) > 200 {
		return echo.NewHTTPError(400, "query too long")
	}
	ctx := c.Request().Context()
	total, err := s.Q.CountUnits(ctx, db.CountUnitsParams{ComponentID: co.ID, Locale: locale, Query: likePattern(q), Status: status})
	if err != nil {
		return err
	}
	rows, err := s.Q.ListUnits(ctx, db.ListUnitsParams{ComponentID: co.ID, Locale: locale, Query: likePattern(q), Status: status, PageLimit: 50, PageOffset: int32(offset)})
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"total": total, "offset": offset, "limit": 50, "units": rows})
}
func (s *Server) unitHistory(c *echo.Context) error {
	uid, err := id(c, "unit")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	u, err := s.Q.GetUnit(ctx, uid)
	if err != nil {
		return err
	}
	co, err := s.Q.GetComponent(ctx, u.ComponentID)
	if err != nil {
		return err
	}
	if err = s.authorize(c, co.ProjectID, "viewer"); err != nil {
		return err
	}
	loc, err := localeCode(c.QueryParam("locale"))
	if err != nil {
		return err
	}
	rows, err := s.Q.UnitHistory(ctx, db.UnitHistoryParams{UnitID: uid, Locale: loc})
	if err != nil {
		return err
	}
	return c.JSON(200, rows)
}
func (s *Server) importFile(c *echo.Context) error {
	co, err := s.component(c, "manager")
	if err != nil {
		return err
	}
	loc, err := localeCode(c.QueryParam("locale"))
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request().Body, formats.MaxSize+1))
	if err != nil {
		return err
	}
	res, err := s.Import(c.Request().Context(), co, loc, raw, user(c).ID)
	if err != nil {
		return err
	}
	if res.Imported == 0 && res.Unknown > 0 {
		return echo.NewHTTPError(400, "no key matches the source catalog; import the source locale first")
	}
	return c.JSON(200, res)
}

// ImportResult summarizes one catalog import.
type ImportResult struct {
	Imported int `json:"imported"`
	Unknown  int `json:"unknown"` // target keys missing from the source catalog (skipped)
	Empty    int `json:"empty"`   // target entries without a value (left untranslated)
}

// Import parses a catalog and merges it. Source-locale imports create or update
// units (changed sources flag translations for review); other locales update
// translations of known keys and register the locale on the project. Keys
// unknown to the source catalog and empty values are skipped and counted, so a
// slightly divergent translation file still imports everything it can.
func (s *Server) Import(ctx context.Context, co db.Component, locale string, raw []byte, uid int64) (ImportResult, error) {
	var res ImportResult
	cat, err := formats.Parse(co.Format, raw)
	if err != nil {
		return res, echo.NewHTTPError(400, err.Error())
	}
	if len(cat.Entries) == 0 || len(cat.Entries) > 20000 {
		return res, echo.NewHTTPError(400, "catalog must contain 1–20000 entries")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)
	// Serialize imports and edits in a component, including concurrent processes.
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", co.ID); err != nil {
		return res, err
	}
	q := s.Q.WithTx(tx)
	p, err := q.GetProject(ctx, co.ProjectID)
	if err != nil {
		return res, err
	}
	if locale != p.SourceLocale {
		if err = q.AddProjectLocale(ctx, db.AddProjectLocaleParams{ProjectID: co.ProjectID, Locale: locale}); err != nil {
			return res, err
		}
	}
	if locale != p.SourceLocale {
		// Unknown keys are re-derived from this file, so drop the previous report.
		if err = q.DeleteImportIssues(ctx, db.DeleteImportIssuesParams{ComponentID: co.ID, Locale: locale}); err != nil {
			return res, err
		}
	}
	for _, entry := range cat.Entries {
		old, findErr := q.FindUnit(ctx, db.FindUnitParams{ComponentID: co.ID, Key: entry.Key})
		if findErr != nil && !errors.Is(findErr, pgx.ErrNoRows) {
			return res, findErr
		}
		if locale == p.SourceLocale {
			source := entry.Value
			if co.Format == "po" {
				source = entry.Key
				if i := strings.IndexByte(source, 4); i >= 0 {
					source = source[i+1:]
				}
			}
			u, e := q.UpsertUnit(ctx, db.UpsertUnitParams{ComponentID: co.ID, Key: entry.Key, Source: source})
			if e != nil {
				return res, e
			}
			if findErr == nil && old.Source != source {
				if e = q.MarkNeedsReview(ctx, u.ID); e != nil {
					return res, e
				}
			}
			res.Imported++
		} else {
			if errors.Is(findErr, pgx.ErrNoRows) {
				res.Unknown++
				value := entry.Value
				if len(value) > 1000 {
					value = value[:1000]
				}
				if e := q.AddImportIssue(ctx, db.AddImportIssueParams{ComponentID: co.ID, Locale: locale, Key: entry.Key, Value: value}); e != nil {
					return res, e
				}
				continue
			}
			if entry.Value == "" {
				res.Empty++
				continue
			}
			if e := q.ImportTranslation(ctx, db.ImportTranslationParams{UnitID: old.ID, Locale: locale, Value: entry.Value, UpdatedBy: pgtype.Int8{Int64: uid, Valid: uid > 0}}); e != nil {
				return res, e
			}
			res.Imported++
		}
	}
	if locale == p.SourceLocale {
		// Keys the source now defines are no longer issues for any locale.
		if err = q.PruneImportIssues(ctx, co.ID); err != nil {
			return res, err
		}
	}
	if err = q.SaveDocument(ctx, db.SaveDocumentParams{ComponentID: co.ID, Locale: locale, Content: raw}); err != nil {
		return res, err
	}
	if err = tx.Commit(ctx); err != nil {
		return res, err
	}
	return res, nil
}
func (s *Server) listImportIssues(c *echo.Context) error {
	co, err := s.component(c, "viewer")
	if err != nil {
		return err
	}
	rows, err := s.Q.ListImportIssues(c.Request().Context(), co.ID)
	if err != nil {
		return err
	}
	return c.JSON(200, rows)
}
func (s *Server) dismissImportIssue(c *echo.Context) error {
	co, err := s.component(c, "manager")
	if err != nil {
		return err
	}
	var in struct{ Locale, Key string }
	if err = decode(c, &in); err != nil {
		return err
	}
	loc, err := localeCode(in.Locale)
	if err != nil {
		return err
	}
	n, err := s.Q.DismissImportIssue(c.Request().Context(), db.DismissImportIssueParams{ComponentID: co.ID, Locale: loc, Key: in.Key})
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]int64{"dismissed": n})
}
func (s *Server) projectImportIssues(c *echo.Context) error {
	pid, err := s.projectID(c, "viewer")
	if err != nil {
		return err
	}
	rows, err := s.Q.ProjectImportIssueCounts(c.Request().Context(), pid)
	if err != nil {
		return err
	}
	return c.JSON(200, rows)
}
func (s *Server) Export(ctx context.Context, co db.Component, locale string) ([]byte, error) {
	p, err := s.Q.GetProject(ctx, co.ProjectID)
	if err != nil {
		return nil, err
	}
	doc, err := s.Q.GetDocument(ctx, db.GetDocumentParams{ComponentID: co.ID, Locale: locale})
	if errors.Is(err, pgx.ErrNoRows) {
		doc, err = s.Q.GetDocument(ctx, db.GetDocumentParams{ComponentID: co.ID, Locale: p.SourceLocale})
	}
	if err != nil {
		return nil, err
	}
	cat, err := formats.Parse(co.Format, doc.Content)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for offset := int32(0); ; offset += 200 {
		rows, e := s.Q.ListUnits(ctx, db.ListUnitsParams{ComponentID: co.ID, Locale: locale, PageLimit: 200, PageOffset: offset})
		if e != nil {
			return nil, e
		}
		for _, r := range rows {
			if locale == p.SourceLocale {
				values[r.Key] = r.Source
			} else {
				values[r.Key] = r.Value
			}
		}
		if len(rows) < 200 {
			break
		}
	}
	return cat.Render(values)
}
func (s *Server) exportFile(c *echo.Context) error {
	co, err := s.component(c, "viewer")
	if err != nil {
		return err
	}
	loc, err := localeCode(c.QueryParam("locale"))
	if err != nil {
		return err
	}
	raw, err := s.Export(c.Request().Context(), co, loc)
	if err != nil {
		return err
	}
	ext := co.Format
	if ext == "android" {
		ext = "xml"
	}
	c.Response().Header().Set("Content-Disposition", `attachment; filename="`+loc+"."+ext+`"`)
	return c.Blob(200, "application/octet-stream", raw)
}
func (s *Server) saveTranslation(c *echo.Context) error {
	uid, err := id(c, "unit")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	u, err := s.Q.GetUnit(ctx, uid)
	if err != nil {
		return err
	}
	co, err := s.Q.GetComponent(ctx, u.ComponentID)
	if err != nil {
		return err
	}
	if err = s.authorize(c, co.ProjectID, "translator"); err != nil {
		return err
	}
	loc, err := localeCode(c.Param("locale"))
	if err != nil {
		return err
	}
	p, err := s.Q.GetProject(ctx, co.ProjectID)
	if err != nil {
		return err
	}
	if loc == p.SourceLocale {
		return echo.NewHTTPError(400, "edit source through catalog import")
	}
	var in struct {
		Value, Status string
		Version       int64
	}
	if err = decode(c, &in); err != nil {
		return err
	}
	if len(in.Value) > 65536 || in.Version < 0 {
		return echo.NewHTTPError(400, "invalid translation")
	}
	if in.Status == "" {
		in.Status = "translated"
	}
	if in.Status != "translated" && in.Status != "reviewed" && in.Status != "needs_review" {
		return echo.NewHTTPError(400, "invalid status")
	}
	if in.Status == "reviewed" {
		if err = s.authorize(c, co.ProjectID, "manager"); err != nil {
			return err
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", co.ID); err != nil {
		return err
	}
	q := s.Q.WithTx(tx)
	if err = q.AddProjectLocale(ctx, db.AddProjectLocaleParams{ProjectID: co.ProjectID, Locale: loc}); err != nil {
		return err
	}
	by := pgtype.Int8{Int64: user(c).ID, Valid: true}
	var row db.Translation
	if in.Version == 0 {
		row, err = q.SaveTranslation(ctx, db.SaveTranslationParams{UnitID: uid, Locale: loc, Value: in.Value, Status: in.Status, UpdatedBy: by, ExpectedVersion: 0})
	} else {
		row, err = q.UpdateTranslation(ctx, db.UpdateTranslationParams{UnitID: uid, Locale: loc, Value: in.Value, Status: in.Status, UpdatedBy: by, ExpectedVersion: in.Version})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(409, "translation changed; reload before saving")
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	return c.JSON(200, row)
}
