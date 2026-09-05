package gitrepo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubRepository(t *testing.T) {
	ok := map[string]string{
		"https://github.com/owner/repo.git": "owner/repo",
		"https://github.com/owner/repo":     "owner/repo",
	}
	for in, want := range ok {
		got, err := GitHubRepository(in)
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "http://github.com/a/b", "https://gitlab.com/a/b", "https://github.com/a", "https://github.com/a/b/c", "https://user@github.com/a/b", "https://github.com/../b", "https://github.com/a/b?x=1", "git@github.com:a/b.git"} {
		if _, err := GitHubRepository(bad); err == nil {
			t.Errorf("expected rejection for %q", bad)
		}
	}
}

func TestValidateBranch(t *testing.T) {
	for _, ok := range []string{"main", "feature/x", "release-1.2", "v1"} {
		if err := ValidateBranch(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-x", "/x", "x/", "a..b", "a b", "a~b", "a^b", "a:b", "a?b", "a*b", "a[b", "a\\b", "@", "a@{b", ".hidden", "x.lock", "a/.b", "a/b/"} {
		if err := ValidateBranch(bad); err == nil {
			t.Errorf("expected rejection for %q", bad)
		}
	}
}

func TestValidatePattern(t *testing.T) {
	for _, ok := range []string{"locales/{locale}.json", "{locale}/strings.xml", "po/{locale}.po"} {
		if err := ValidatePattern(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"locales/en.json", "{locale}/{locale}.json", "../{locale}.json", "/abs/{locale}.json", ".git/{locale}", "a/./{locale}", "a\\{locale}", "{locale}:x"} {
		if err := ValidatePattern(bad); err == nil {
			t.Errorf("expected rejection for %q", bad)
		}
	}
}

func TestLocalRepositoryReadWrite(t *testing.T) {
	root := t.TempDir()
	r := Repository{Root: root, ID: 7, URL: "https://github.com/a/b.git", Branch: "main"}
	if _, err := r.SafePath("x/y.json"); err == nil {
		t.Fatal("expected error when checkout directory is missing")
	}
	if err := writeDir(r.Dir()); err != nil {
		t.Fatal(err)
	}
	if err := r.Write("locales/en.json", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	got, err := r.Read("locales/en.json")
	if err != nil || string(got) != "{}" {
		t.Fatalf("read: %q %v", got, err)
	}
	if _, err = r.Read("../outside"); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	r := Repository{Root: root, ID: 3}
	files := map[string]string{
		"web/locales/en.json":                    "{}",
		"web/locales/ja.json":                    "{}",
		"web/locales/index.json":                 "{}",
		"i18n/en/translation.json":               "{}",
		"i18n/de/translation.json":               "{}",
		"app/src/main/res/values-ja/strings.xml": "<resources/>",
		"app/src/main/res/values/strings.xml":    "<resources/>",
		"po/fr.po":                               "",
		"node_modules/x/locales/en.json":         "{}",
		"README.md":                              "",
	}
	for p, content := range files {
		full := filepath.Join(r.Dir(), filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.Scan()
	if err != nil {
		t.Fatal(err)
	}
	want := []Candidate{
		{Pattern: "app/src/main/res/values-{locale}/strings.xml", Format: "android", Locales: []string{"ja"}},
		{Pattern: "i18n/{locale}/translation.json", Format: "json", Locales: []string{"de", "en"}},
		{Pattern: "po/{locale}.po", Format: "po", Locales: []string{"fr"}},
		{Pattern: "web/locales/{locale}.json", Format: "json", Locales: []string{"en", "ja"}},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i].Pattern != want[i].Pattern || got[i].Format != want[i].Format || strings.Join(got[i].Locales, ",") != strings.Join(want[i].Locales, ",") {
			t.Errorf("candidate %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}
