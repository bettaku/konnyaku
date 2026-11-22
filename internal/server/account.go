package server

import (
	"github.com/labstack/echo/v5"
	"golang.org/x/crypto/bcrypt"
	"konnyaku/internal/db"
)

// Self-service account endpoints: every signed-in user may change their own
// display name and password and sign out their other sessions.

// currentToken returns the hashed token of the session making the request.
// authenticate has already validated the cookie.
func currentToken(c *echo.Context) string {
	ck, _ := c.Cookie("session")
	return hash(ck.Value)
}

func (s *Server) updateMe(c *echo.Context) error {
	var in struct{ Name string }
	if err := decode(c, &in); err != nil {
		return err
	}
	if !validName(in.Name) {
		return echo.NewHTTPError(400, "name must be 1–200 characters")
	}
	u, err := s.Q.UpdateUserName(c.Request().Context(), db.UpdateUserNameParams{ID: user(c).ID, Name: in.Name})
	if err != nil {
		return err
	}
	return c.JSON(200, u)
}

func (s *Server) changePassword(c *echo.Context) error {
	// Verifying the current password costs a bcrypt round, so share the login throttle.
	select {
	case s.loginSlots <- struct{}{}:
		defer func() { <-s.loginSlots }()
	default:
		return echo.NewHTTPError(429, "too many requests")
	}
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}
	if len(in.New) < 12 || len(in.New) > 72 {
		return echo.NewHTTPError(400, "new password must be 12–72 bytes")
	}
	ctx := c.Request().Context()
	u, err := s.Q.GetUserByID(ctx, user(c).ID)
	if err != nil {
		return err
	}
	if len(in.Current) > 72 || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.Current)) != nil {
		return echo.NewHTTPError(403, "current password is incorrect")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(in.New), 12)
	if err != nil {
		return err
	}
	if err := s.Q.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{ID: u.ID, PasswordHash: string(h)}); err != nil {
		return err
	}
	// A password change should lock out anyone holding an older session; keep this one.
	n, err := s.Q.DeleteOtherSessions(ctx, db.DeleteOtherSessionsParams{UserID: u.ID, TokenHash: currentToken(c)})
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]int64{"signed_out": n})
}

func (s *Server) signOutOthers(c *echo.Context) error {
	n, err := s.Q.DeleteOtherSessions(c.Request().Context(), db.DeleteOtherSessionsParams{UserID: user(c).ID, TokenHash: currentToken(c)})
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]int64{"signed_out": n})
}
