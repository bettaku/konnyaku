package server

import (
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"
	"konnyaku/internal/integrations"
)

func pgInt8(v int64) pgtype.Int8 { return pgtype.Int8{Int64: v, Valid: true} }

func (s *Server) suggest(c *echo.Context) error {
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
	var in struct{ Provider, Locale string }
	if err = decode(c, &in); err != nil {
		return err
	}
	loc, err := localeCode(in.Locale)
	if err != nil {
		return err
	}
	p, err := s.Q.GetProject(ctx, co.ProjectID)
	if err != nil {
		return err
	}
	if loc == p.SourceLocale {
		return echo.NewHTTPError(400, "choose a target locale")
	}
	if len(u.Source) > 16000 {
		return echo.NewHTTPError(400, "source too long for machine translation")
	}
	var result string
	switch in.Provider {
	case "openai":
		result, err = (integrations.OpenAI{BaseURL: s.Config.OpenAIBaseURL, Key: s.Config.OpenAIKey, Model: s.Config.OpenAIModel}).Translate(ctx, u.Source, p.SourceLocale, loc)
	case "google":
		result, err = (integrations.Google{Project: s.Config.GoogleProject, Location: s.Config.GoogleLocation}).Translate(ctx, u.Source, p.SourceLocale, loc)
	default:
		return echo.NewHTTPError(400, "unknown provider")
	}
	if err != nil {
		return echo.NewHTTPError(502, err.Error())
	}
	return c.JSON(200, map[string]string{"value": result, "provider": in.Provider})
}
