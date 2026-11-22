package server

import "testing"

func TestAccountSelfService(t *testing.T) {
	_, ts := newTestServer(t)
	me := login(t, ts.URL, "trans@example.com", "translator-pass-1")
	other := login(t, ts.URL, "trans@example.com", "translator-pass-1")
	admin := login(t, ts.URL, "admin@example.com", "admin-password-123")

	// Display name.
	if got := me.must(200, "PATCH", "/api/me", map[string]string{"name": "Translator Two"}); got["name"] != "Translator Two" || got["admin"] != false {
		t.Fatalf("rename: %v", got)
	}
	if got := me.must(200, "GET", "/api/me", nil); got["name"] != "Translator Two" {
		t.Fatalf("/me after rename: %v", got)
	}
	me.must(400, "PATCH", "/api/me", map[string]string{"name": "   "})
	// Only the name is editable; unknown fields such as admin are rejected.
	me.must(400, "PATCH", "/api/me", map[string]any{"name": "x", "admin": true})

	// Password change validates the current password and the new length.
	me.must(403, "POST", "/api/me/password", map[string]string{"current_password": "wrong-password-xx", "new_password": "a-brand-new-pass"})
	me.must(400, "POST", "/api/me/password", map[string]string{"current_password": "translator-pass-1", "new_password": "short"})
	if got := me.must(200, "POST", "/api/me/password", map[string]string{"current_password": "translator-pass-1", "new_password": "a-brand-new-pass"}); got["signed_out"] != float64(1) {
		t.Fatalf("password change should sign out the other session: %v", got)
	}
	me.must(200, "GET", "/api/me", nil)
	other.must(401, "GET", "/api/me", nil)
	admin.must(200, "GET", "/api/me", nil) // other users' sessions are untouched
	anon := &client{t: t, base: ts.URL}
	anon.must(401, "POST", "/api/login", map[string]string{"email": "trans@example.com", "password": "translator-pass-1"})
	again := login(t, ts.URL, "trans@example.com", "a-brand-new-pass")

	// Signing out other sessions keeps the caller's.
	if got := me.must(200, "POST", "/api/me/sessions/sign-out-others", nil); got["signed_out"] != float64(1) {
		t.Fatalf("sign out others: %v", got)
	}
	me.must(200, "GET", "/api/me", nil)
	again.must(401, "GET", "/api/me", nil)
	anon.must(401, "PATCH", "/api/me", map[string]string{"name": "x"})
}
