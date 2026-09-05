package gitrepo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseLocaleName(t *testing.T) {
	ok := map[string]string{"ja": "ja", "en_US": "en-US", "zh-rCN": "zh-CN", "b+sr+Latn": "sr-Latn", "pt-BR": "pt-BR", "fil": "fil"}
	for in, want := range ok {
		got, name, k := ParseLocaleName(in)
		if !k || got != want || name == "" {
			t.Errorf("%s: got %q %q %v", in, got, name, k)
		}
	}
	for _, bad := range []string{"", "index", "strings", "values", "app", "root", "translation", "default"} {
		if _, _, k := ParseLocaleName(bad); k {
			t.Errorf("%q should not be a locale", bad)
		}
	}
}

func TestLocaleFilesAndFind(t *testing.T) {
	r := Repository{Root: t.TempDir(), ID: 1}
	for _, p := range []string{"locales/en.json", "locales/ja.json", "locales/pt_BR.json", "locales/index.json", "i18n/de/app.json", "i18n/fr-CA/app.json", "i18n/fr-CA/other.json", "res/values/strings.xml", "res/values-ja/strings.xml", "res/values-zh-rCN/strings.xml", "res/values-night/strings.xml"} {
		full := filepath.Join(r.Dir(), filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := r.LocaleFiles("locales/{locale}.json")
	if err != nil || len(files) != 4 {
		t.Fatalf("locales: %+v %v", files, err)
	}
	for _, f := range files {
		if f.Recognized() == (f.Raw == "index") {
			t.Errorf("recognition: %+v", f)
		}
	}
	if f := FindLocaleFile(files, "ja-JP"); f == nil || f.Path != "locales/ja.json" {
		t.Errorf("ja-JP should fall back to ja.json: %+v", f)
	}
	if f := FindLocaleFile(files, "pt"); f == nil || f.Path != "locales/pt_BR.json" || f.Code != "pt-BR" {
		t.Errorf("pt should match pt_BR.json: %+v", f)
	}
	if f := FindLocaleFile(files, "de"); f != nil {
		t.Errorf("de should not match: %+v", f)
	}
	if got := LocalePath("locales/{locale}.json", "es-MX", files); got != "locales/es_MX.json" {
		t.Errorf("underscore convention not followed: %s", got)
	}
	files, err = r.LocaleFiles("i18n/{locale}/app.json")
	if err != nil || len(files) != 2 || FindLocaleFile(files, "fr").Path != "i18n/fr-CA/app.json" {
		t.Fatalf("directory layout: %+v %v", files, err)
	}
	files, err = r.LocaleFiles("res/values-{locale}/strings.xml")
	if err != nil || len(files) != 4 {
		t.Fatalf("android: %+v %v", files, err)
	}
	if f := FindLocaleFile(files, "en-US"); f == nil || !f.Default || f.Path != "res/values/strings.xml" {
		t.Errorf("source should use the default file: %+v", f)
	}
	if f := FindLocaleFile(files, "zh-CN"); f == nil || f.Path != "res/values-zh-rCN/strings.xml" {
		t.Errorf("android region: %+v", f)
	}
	if got := LocalePath("res/values-{locale}/strings.xml", "pt-BR", files); got != "res/values-pt-rBR/strings.xml" {
		t.Errorf("android path: %s", got)
	}
	if files, err = r.LocaleFiles("missing/{locale}.json"); err != nil || files != nil {
		t.Errorf("missing dir: %+v %v", files, err)
	}
}
