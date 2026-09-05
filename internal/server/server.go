package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"golang.org/x/crypto/bcrypt"
	"konnyaku/internal/config"
	"konnyaku/internal/db"
	"konnyaku/web"
)

type Server struct {
	Pool       *pgxpool.Pool
	Q          *db.Queries
	Config     config.Config
	Echo       *echo.Echo
	loginSlots chan struct{}
}

func New(pool *pgxpool.Pool, cfg config.Config) *Server {
	s := &Server{Pool: pool, Q: db.New(pool), Config: cfg, loginSlots: make(chan struct{}, 4)}
	e := echo.NewWithConfig(echo.Config{IPExtractor: echo.ExtractIPDirect()})
	s.Echo = e
	e.Use(middleware.Recover(), middleware.BodyLimit(5<<20))
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			h := c.Response().Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "same-origin")
			h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
			h.Set("Cache-Control", "no-store")
			if strings.HasPrefix(c.Path(), "/api/") && c.Request().Method != "GET" && c.Request().Method != "HEAD" {
				if c.Request().Header.Get("X-Requested-With") != "konnyaku" {
					return echo.NewHTTPError(403, "missing request protection header")
				}
				if origin := c.Request().Header.Get("Origin"); origin != "" && origin != cfg.PublicURL {
					return echo.NewHTTPError(403, "invalid origin")
				}
			}
			return next(c)
		}
	})
	e.HTTPErrorHandler = func(c *echo.Context, err error) {
		code, message := 500, "internal server error"
		var he *echo.HTTPError
		var pe *pgconn.PgError
		if errors.As(err, &he) {
			code = he.Code
			message = he.Message
		} else if errors.Is(err, pgx.ErrNoRows) {
			code = 404
			message = "not found"
		} else if errors.As(err, &pe) {
			switch pe.Code {
			case "23505":
				code = 409
				message = "already exists"
			case "23503", "23514", "22001":
				code = 400
				message = "invalid or referenced record"
			}
		}
		if code == 500 {
			e.Logger.Error("request failed", "method", c.Request().Method, "path", c.Path(), "error", err)
		}
		_ = c.JSON(code, map[string]string{"error": message})
	}
	e.GET("/healthz", func(c *echo.Context) error { return c.JSON(200, map[string]string{"status": "ok"}) })
	e.GET("/readyz", func(c *echo.Context) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			return echo.NewHTTPError(503, "database unavailable")
		}
		return c.NoContent(204)
	})
	e.GET("/", func(c *echo.Context) error { return c.FileFS("dist/index.html", web.Files) })
	e.GET("/favicon.ico", func(c *echo.Context) error { return c.NoContent(204) })
	e.GET("/assets/*", func(c *echo.Context) error {
		name := c.Param("*")
		if strings.Contains(name, "..") || strings.Contains(name, "/") {
			return echo.NewHTTPError(404, "not found")
		}
		c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return c.FileFS("dist/assets/"+name, web.Files)
	})
	e.POST("/api/login", s.login)
	e.POST("/webhooks/github", s.webhook)
	api := e.Group("/api", s.authenticate)
	api.GET("/me", func(c *echo.Context) error { return c.JSON(200, user(c)) })
	api.POST("/logout", s.logout)
	api.GET("/users", s.listUsers, s.admin)
	api.POST("/users", s.createUser, s.admin)
	api.GET("/locales", s.listLocales)
	api.POST("/locales", s.saveLocale, s.admin)
	api.DELETE("/locales/:code", s.deleteLocale, s.admin)
	api.GET("/projects", s.listProjects)
	api.POST("/projects", s.createProject, s.admin)
	api.GET("/projects/:project", s.getProject)
	api.PATCH("/projects/:project", s.renameProject)
	api.DELETE("/projects/:project", s.deleteProject)
	api.GET("/projects/:project/stats", s.projectStats)
	api.GET("/projects/:project/history", s.projectHistory)
	api.GET("/projects/:project/issues", s.projectImportIssues)
	api.GET("/projects/:project/locales", s.projectLocales)
	api.PUT("/projects/:project/locales/:locale", s.addProjectLocale)
	api.DELETE("/projects/:project/locales/:locale", s.removeProjectLocale)
	api.GET("/projects/:project/members", s.listMembers)
	api.PUT("/projects/:project/members/:member", s.saveMember)
	api.DELETE("/projects/:project/members/:member", s.deleteMember)
	api.GET("/projects/:project/components", s.listComponents)
	api.POST("/projects/:project/components", s.createComponent)
	api.GET("/projects/:project/repositories", s.listRepositories)
	api.POST("/projects/:project/repositories", s.createRepository, s.admin)
	api.GET("/repositories/:repository", s.repositoryStatus)
	api.DELETE("/repositories/:repository", s.deleteRepository, s.admin)
	api.GET("/repositories/:repository/scan", s.scanRepository)
	api.POST("/repositories/:repository/git/:action", s.repositoryAction)
	api.POST("/repositories/:repository/pull-request", s.repositoryPullRequest, s.admin)
	api.GET("/components/:component", s.getComponent)
	api.PATCH("/components/:component", s.updateComponent)
	api.DELETE("/components/:component", s.deleteComponent)
	api.GET("/components/:component/stats", s.componentStats)
	api.GET("/components/:component/history", s.componentHistory)
	api.GET("/components/:component/issues", s.listImportIssues)
	api.POST("/components/:component/issues/dismiss", s.dismissImportIssue)
	api.GET("/components/:component/units", s.listUnits)
	api.POST("/components/:component/import", s.importFile)
	api.GET("/components/:component/export", s.exportFile)
	api.GET("/projects/:project/glossary", s.listGlossary)
	api.POST("/projects/:project/glossary", s.saveGlossaryTerm)
	api.GET("/projects/:project/glossary/export", s.exportGlossary)
	api.POST("/projects/:project/glossary/import", s.importGlossary)
	api.DELETE("/projects/:project/glossary/:term", s.deleteGlossaryTerm)
	api.POST("/components/:component/autofill", s.autofill)
	api.GET("/units/:unit/assist", s.assist)
	api.GET("/units/:unit/history", s.unitHistory)
	api.PUT("/units/:unit/translations/:locale", s.saveTranslation)
	api.POST("/units/:unit/suggest", s.suggest)
	api.GET("/deliveries", func(c *echo.Context) error {
		rows, err := s.Q.ListDeliveries(c.Request().Context())
		if err != nil {
			return err
		}
		return c.JSON(200, rows)
	}, s.admin)
	api.POST("/deliveries/:delivery/retry", func(c *echo.Context) error {
		n, err := s.Q.RetryDelivery(c.Request().Context(), c.Param("delivery"))
		if err != nil {
			return err
		}
		if n == 0 {
			return echo.NewHTTPError(409, "only failed deliveries can be retried")
		}
		return c.NoContent(204)
	}, s.admin)
	return s
}
func decode(c *echo.Context, v any) error {
	if strings.Split(c.Request().Header.Get("Content-Type"), ";")[0] != "application/json" {
		return echo.NewHTTPError(415, "expected application/json")
	}
	d := json.NewDecoder(c.Request().Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return echo.NewHTTPError(400, "invalid JSON body")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return echo.NewHTTPError(400, "expected one JSON value")
	}
	return nil
}
func id(c *echo.Context, key string) (int64, error) {
	v, err := strconv.ParseInt(c.Param(key), 10, 64)
	if err != nil || v < 1 {
		return 0, echo.NewHTTPError(400, "invalid ID")
	}
	return v, nil
}
func user(c *echo.Context) db.SessionUserRow { return c.Get("user").(db.SessionUserRow) }
func hash(s string) string                   { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func (s *Server) authenticate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ck, err := c.Cookie("session")
		if err != nil || len(ck.Value) != 64 {
			return echo.NewHTTPError(401, "login required")
		}
		u, err := s.Q.SessionUser(c.Request().Context(), hash(ck.Value))
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(401, "login required")
		}
		if err != nil {
			return err
		}
		c.Set("user", u)
		return next(c)
	}
}
func (s *Server) admin(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if !user(c).Admin {
			return echo.NewHTTPError(403, "administrator required")
		}
		return next(c)
	}
}
func (s *Server) authorize(c *echo.Context, pid int64, min string) error {
	if _, err := s.Q.GetProject(c.Request().Context(), pid); err != nil {
		return err
	}
	if user(c).Admin {
		return nil
	}
	role, err := s.Q.GetRole(c.Request().Context(), db.GetRoleParams{ProjectID: pid, UserID: user(c).ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(404, "not found")
	}
	if err != nil {
		return err
	}
	ranks := map[string]int{"viewer": 1, "translator": 2, "manager": 3}
	if ranks[role] < ranks[min] {
		return echo.NewHTTPError(403, "insufficient project permission")
	}
	return nil
}
func (s *Server) component(c *echo.Context, min string) (db.Component, error) {
	cid, err := id(c, "component")
	if err != nil {
		return db.Component{}, err
	}
	co, err := s.Q.GetComponent(c.Request().Context(), cid)
	if err != nil {
		return co, err
	}
	return co, s.authorize(c, co.ProjectID, min)
}
func (s *Server) login(c *echo.Context) error {
	select {
	case s.loginSlots <- struct{}{}:
		defer func() { <-s.loginSlots }()
	default:
		return echo.NewHTTPError(429, "too many login requests")
	}
	var in struct{ Email, Password string }
	if err := decode(c, &in); err != nil {
		return err
	}
	if len(in.Password) > 72 || len(in.Email) > 254 {
		return echo.NewHTTPError(401, "invalid credentials")
	}
	u, err := s.Q.GetUserByEmail(c.Request().Context(), strings.ToLower(strings.TrimSpace(in.Email)))
	// Always pay the bcrypt cost even for unknown users.
	digest := u.PasswordHash
	if errors.Is(err, pgx.ErrNoRows) {
		digest = "$2a$12$R9h/cIPz0gi.URNNX3kh2OPST9/PgBkqquzi.Ss7KIUgO2t0jWMUW"
	} else if err != nil {
		return err
	}
	mismatch := bcrypt.CompareHashAndPassword([]byte(digest), []byte(in.Password))
	if mismatch != nil || err != nil {
		return echo.NewHTTPError(401, "invalid credentials")
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		return err
	}
	value := hex.EncodeToString(token)
	expires := time.Now().Add(24 * time.Hour)
	if err = s.Q.CreateSession(c.Request().Context(), db.CreateSessionParams{TokenHash: hash(value), UserID: u.ID, ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}}); err != nil {
		return err
	}
	s.cookie(c, value, 86400)
	return c.JSON(200, map[string]any{"id": u.ID, "email": u.Email, "name": u.Name, "admin": u.Admin})
}
func (s *Server) cookie(c *echo.Context, value string, maxAge int) {
	c.SetCookie(&http.Cookie{Name: "session", Value: value, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.Config.PublicURL, "https://"), SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}
func (s *Server) logout(c *echo.Context) error {
	ck, _ := c.Cookie("session")
	if err := s.Q.DeleteSession(c.Request().Context(), hash(ck.Value)); err != nil {
		return err
	}
	s.cookie(c, "", -1)
	return c.NoContent(204)
}
