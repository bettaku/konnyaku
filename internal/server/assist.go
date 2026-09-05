package server

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"
	"konnyaku/internal/db"
)

// assist returns translation-memory matches and glossary hits for one unit.
func (s *Server) assist(c *echo.Context) error {
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
	memory := []db.TranslationMemoryRow{}
	if len(u.Source) <= 4000 && strings.TrimSpace(u.Source) != "" {
		memory, err = s.Q.TranslationMemory(ctx, db.TranslationMemoryParams{Source: u.Source, Locale: loc, UnitID: uid, IsAdmin: user(c).Admin, UserID: user(c).ID})
		if err != nil {
			return err
		}
	}
	// Exact matches first, then by similarity, then reviewed before translated.
	sort.SliceStable(memory, func(i, j int) bool {
		if (memory[i].Source == u.Source) != (memory[j].Source == u.Source) {
			return memory[i].Source == u.Source
		}
		if memory[i].Score != memory[j].Score {
			return memory[i].Score > memory[j].Score
		}
		return memory[i].Status == "reviewed" && memory[j].Status != "reviewed"
	})
	if len(memory) > 10 {
		memory = memory[:10]
	}
	glossary, err := s.Q.GlossaryMatches(ctx, db.GlossaryMatchesParams{ProjectID: co.ProjectID, Locale: loc, Source: u.Source})
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"memory": memory, "glossary": glossary})
}
func (s *Server) listGlossary(c *echo.Context) error {
	pid, err := s.projectID(c, "viewer")
	if err != nil {
		return err
	}
	loc := ""
	if v := c.QueryParam("locale"); v != "" {
		if loc, err = localeCode(v); err != nil {
			return err
		}
	}
	rows, err := s.Q.ListGlossary(c.Request().Context(), db.ListGlossaryParams{ProjectID: pid, Locale: loc})
	if err != nil {
		return err
	}
	return c.JSON(200, rows)
}
func (s *Server) saveGlossaryTerm(c *echo.Context) error {
	pid, err := s.projectID(c, "translator")
	if err != nil {
		return err
	}
	var in struct{ Locale, Term, Translation, Note string }
	if err = decode(c, &in); err != nil {
		return err
	}
	loc, err := localeCode(in.Locale)
	if err != nil {
		return err
	}
	in.Term = strings.TrimSpace(in.Term)
	in.Translation = strings.TrimSpace(in.Translation)
	if in.Term == "" || len(in.Term) > 200 || in.Translation == "" || len(in.Translation) > 500 || len(in.Note) > 1000 {
		return echo.NewHTTPError(400, "term (≤200) and translation (≤500) required")
	}
	p, err := s.Q.GetProject(c.Request().Context(), pid)
	if err != nil {
		return err
	}
	if loc == p.SourceLocale {
		return echo.NewHTTPError(400, "glossary entries target a translation locale")
	}
	row, err := s.Q.SaveGlossaryTerm(c.Request().Context(), db.SaveGlossaryTermParams{ProjectID: pid, Locale: loc, Term: in.Term, Translation: in.Translation, Note: in.Note, UpdatedBy: pgtype.Int8{Int64: user(c).ID, Valid: true}})
	if err != nil {
		return err
	}
	return c.JSON(200, row)
}
func (s *Server) deleteGlossaryTerm(c *echo.Context) error {
	pid, err := s.projectID(c, "manager")
	if err != nil {
		return err
	}
	tid, err := id(c, "term")
	if err != nil {
		return err
	}
	n, err := s.Q.DeleteGlossaryTerm(c.Request().Context(), db.DeleteGlossaryTermParams{ID: tid, ProjectID: pid})
	if err != nil {
		return err
	}
	if n == 0 {
		return echo.NewHTTPError(404, "not found")
	}
	return c.NoContent(204)
}

// autofill copies exact translation-memory matches into untranslated units.
// With dry_run it only reports how many units could be filled.
func (s *Server) autofill(c *echo.Context) error {
	co, err := s.component(c, "translator")
	if err != nil {
		return err
	}
	var in struct {
		Locale, Status string
		DryRun         bool `json:"dry_run"`
	}
	if err = decode(c, &in); err != nil {
		return err
	}
	loc, err := localeCode(in.Locale)
	if err != nil {
		return err
	}
	if in.Status == "" {
		in.Status = "needs_review"
	}
	if in.Status != "needs_review" && in.Status != "translated" {
		return echo.NewHTTPError(400, "status must be needs_review or translated")
	}
	ctx := c.Request().Context()
	p, err := s.Q.GetProject(ctx, co.ProjectID)
	if err != nil {
		return err
	}
	if loc == p.SourceLocale {
		return echo.NewHTTPError(400, "choose a target locale")
	}
	rows, err := s.Q.ExactMemoryMatches(ctx, db.ExactMemoryMatchesParams{Locale: loc, IsAdmin: user(c).Admin, UserID: user(c).ID, ComponentID: co.ID})
	if err != nil {
		return err
	}
	matches := rows[:0]
	for _, r := range rows {
		if r.Value != "" {
			matches = append(matches, r)
		}
	}
	result := map[string]any{"untranslated": len(rows), "matches": len(matches), "filled": 0}
	if in.DryRun || len(matches) == 0 {
		return c.JSON(200, result)
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
	filled := 0
	for _, m := range matches {
		_, err = q.SaveTranslation(ctx, db.SaveTranslationParams{UnitID: m.UnitID, Locale: loc, Value: m.Value, Status: in.Status, UpdatedBy: by, ExpectedVersion: 0})
		if errors.Is(err, pgx.ErrNoRows) {
			continue // translated concurrently
		}
		if err != nil {
			return err
		}
		filled++
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	result["filled"] = filled
	return c.JSON(200, result)
}

// ---- glossary CSV --------------------------------------------------------------

func (s *Server) exportGlossary(c *echo.Context) error {
	pid, err := s.projectID(c, "viewer")
	if err != nil {
		return err
	}
	loc := ""
	if v := c.QueryParam("locale"); v != "" {
		if loc, err = localeCode(v); err != nil {
			return err
		}
	}
	rows, err := s.Q.ListGlossary(c.Request().Context(), db.ListGlossaryParams{ProjectID: pid, Locale: loc})
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"term", "locale", "translation", "note"})
	for _, r := range rows {
		_ = w.Write([]string{r.Term, r.Locale, r.Translation, r.Note})
	}
	w.Flush()
	name := "glossary.csv"
	if loc != "" {
		name = "glossary-" + loc + ".csv"
	}
	c.Response().Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	return c.Blob(200, "text/csv; charset=utf-8", buf.Bytes())
}

// importGlossary upserts rows from a CSV with a header line. Columns term,
// translation and note are matched by name; locale comes from the column or,
// when absent, from the ?locale= query parameter.
func (s *Server) importGlossary(c *echo.Context) error {
	pid, err := s.projectID(c, "translator")
	if err != nil {
		return err
	}
	defaultLocale := ""
	if v := c.QueryParam("locale"); v != "" {
		if defaultLocale, err = localeCode(v); err != nil {
			return err
		}
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request().Body, 1<<20+1))
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return echo.NewHTTPError(413, "CSV exceeds 1 MiB")
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	r := csv.NewReader(bytes.NewReader(raw))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	records, err := r.ReadAll()
	if err != nil {
		return echo.NewHTTPError(400, "invalid CSV: "+err.Error())
	}
	if len(records) < 2 {
		return echo.NewHTTPError(400, "CSV needs a header line and at least one row")
	}
	if len(records) > 10001 {
		return echo.NewHTTPError(400, "CSV is limited to 10000 rows")
	}
	col := map[string]int{}
	for i, h := range records[0] {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	termCol, okT := col["term"]
	trCol, okV := col["translation"]
	if !okT || !okV {
		return echo.NewHTTPError(400, "CSV header must include term and translation (optional: locale, note)")
	}
	locCol, hasLoc := col["locale"]
	noteCol, hasNote := col["note"]
	if !hasLoc && defaultLocale == "" {
		return echo.NewHTTPError(400, "add a locale column or pass ?locale=")
	}
	ctx := c.Request().Context()
	p, err := s.Q.GetProject(ctx, pid)
	if err != nil {
		return err
	}
	field := func(rec []string, i int) string {
		if i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := s.Q.WithTx(tx)
	by := pgtype.Int8{Int64: user(c).ID, Valid: true}
	imported, skipped := 0, 0
	for n, rec := range records[1:] {
		term, tr := field(rec, termCol), field(rec, trCol)
		if term == "" && tr == "" {
			skipped++
			continue
		}
		loc := defaultLocale
		if hasLoc && field(rec, locCol) != "" {
			if loc, err = localeCode(field(rec, locCol)); err != nil {
				return echo.NewHTTPError(400, "row "+strconv.Itoa(n+2)+": invalid locale")
			}
		}
		note := ""
		if hasNote {
			note = field(rec, noteCol)
		}
		if term == "" || len(term) > 200 || tr == "" || len(tr) > 500 || len(note) > 1000 || loc == "" || loc == p.SourceLocale {
			return echo.NewHTTPError(400, "row "+strconv.Itoa(n+2)+": term (≤200), translation (≤500) and a target locale are required")
		}
		if _, err = q.SaveGlossaryTerm(ctx, db.SaveGlossaryTermParams{ProjectID: pid, Locale: loc, Term: term, Translation: tr, Note: note, UpdatedBy: by}); err != nil {
			var pe *pgconn.PgError
			if errors.As(err, &pe) && pe.Code == "23503" {
				return echo.NewHTTPError(400, "row "+strconv.Itoa(n+2)+": locale "+loc+" is not defined")
			}
			return err
		}
		imported++
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	return c.JSON(200, map[string]int{"imported": imported, "skipped": skipped})
}
