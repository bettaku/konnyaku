package server

import (
	"sort"
	"strings"

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
