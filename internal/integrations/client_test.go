package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidSignature(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	body := []byte(`{"ref":"refs/heads/main"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !ValidSignature(secret, sig, body) {
		t.Fatal("valid signature rejected")
	}
	if ValidSignature(secret, sig, []byte("tampered")) || ValidSignature("", sig, body) || ValidSignature(secret, "sha1=abc", body) || ValidSignature(secret, "sha256=zz", body) {
		t.Fatal("invalid signature accepted")
	}
}

func TestOpenAITranslate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("unexpected request %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var in struct {
			Model    string
			Messages []struct{ Role, Content string }
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Model != "m" || len(in.Messages) != 2 || in.Messages[1].Content != "Hello" {
			t.Errorf("unexpected body %+v", in)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": "こんにちは"}}}})
	}))
	defer srv.Close()
	got, err := (OpenAI{BaseURL: srv.URL + "/v1", Key: "k", Model: "m", HTTP: srv.Client()}).Translate(context.Background(), "Hello", "en", "ja")
	if err != nil || got != "こんにちは" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err = (OpenAI{BaseURL: srv.URL, Key: "k"}).Translate(context.Background(), "x", "en", "ja"); err == nil {
		t.Fatal("expected missing model error")
	}
}

func TestGoogleTranslate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"translations": []map[string]string{{"translatedText": "Bonjour"}}})
	}))
	defer srv.Close()
	// Redirect the fixed Google endpoint to the test server through a custom transport.
	client := &http.Client{Transport: rewrite{srv.URL, srv.Client().Transport}}
	got, err := (Google{Project: "p", Location: "global", HTTP: client}).Translate(context.Background(), "Hello", "en", "fr")
	if err != nil || got != "Bonjour" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err = (Google{Project: "bad/project", Location: "global", HTTP: client}).Translate(context.Background(), "x", "en", "fr"); err == nil {
		t.Fatal("expected invalid project rejection")
	}
}

func TestCreatePR(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/pulls" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]string{"html_url": "https://github.com/o/r/pull/1"})
	}))
	defer srv.Close()
	client := &http.Client{Transport: rewrite{srv.URL, srv.Client().Transport}}
	got, err := CreatePR(context.Background(), client, "tok", "o/r", PullRequest{Title: "t", Head: "h", Base: "main"})
	if err != nil || got != "https://github.com/o/r/pull/1" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err = CreatePR(context.Background(), client, "", "o/r", PullRequest{}); err == nil {
		t.Fatal("expected token error")
	}
}

type rewrite struct {
	target string
	next   http.RoundTripper
}

func (rw rewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	u := *r.URL
	u.Scheme = "http"
	u.Host = rw.target[len("http://"):]
	r2 := r.Clone(r.Context())
	r2.URL = &u
	r2.Host = u.Host
	return rw.next.RoundTrip(r2)
}
