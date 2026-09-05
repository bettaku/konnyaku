package formats

import (
	"strings"
	"testing"
)

func entriesMap(c *Catalog) map[string]string {
	m := map[string]string{}
	for _, e := range c.Entries {
		m[e.Key] = e.Value
	}
	return m
}

func TestJSONRoundTrip(t *testing.T) {
	raw := []byte("{\"greeting\": \"Hello\", \"nested\": {\"a/b\": \"x\", \"tilde~\": \"y\"}}")
	c, err := Parse("json", raw)
	if err != nil {
		t.Fatal(err)
	}
	m := entriesMap(c)
	if m["/greeting"] != "Hello" || m["/nested/a~1b"] != "x" || m["/nested/tilde~0"] != "y" {
		t.Fatalf("unexpected entries: %v", m)
	}
	out, err := c.Render(map[string]string{"/greeting": "こんにちは", "/nested/a~1b": "z"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Parse("json", out)
	if err != nil {
		t.Fatal(err)
	}
	m2 := entriesMap(c2)
	if m2["/greeting"] != "こんにちは" || m2["/nested/a~1b"] != "z" || m2["/nested/tilde~0"] != "y" {
		t.Fatalf("render lost values: %s", out)
	}
}

func TestJSONRejects(t *testing.T) {
	for _, raw := range []string{`[1]`, `{"a": 1}`, `{"a": ["x"]}`, `{"a": null}`, `{"a": "x"} {"b": "y"}`, `{"a": "x", "a": "y"}`, `not json`} {
		if _, err := Parse("json", []byte(raw)); err == nil {
			t.Errorf("expected rejection for %s", raw)
		}
	}
}

func TestYAMLRoundTrip(t *testing.T) {
	raw := []byte("# comment\ntitle: Title\nmenu:\n  file: File\n  edit: Edit\n")
	c, err := Parse("yaml", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Entries) != 3 || entriesMap(c)["/menu/edit"] != "Edit" {
		t.Fatalf("unexpected entries: %+v", c.Entries)
	}
	out, err := c.Render(map[string]string{"/menu/file": "ファイル"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "# comment") || !strings.Contains(s, "file: ファイル") || !strings.Contains(s, "edit: Edit") {
		t.Fatalf("unexpected yaml output:\n%s", s)
	}
}

func TestPORoundTrip(t *testing.T) {
	raw := []byte(`# Translator comment
msgid ""
msgstr ""
"Content-Type: text/plain; charset=UTF-8\n"

#: src/main.c:1
msgid "Hello"
msgstr ""

msgctxt "menu"
msgid "File"
msgstr "Fichier"

msgid "Multi"
"line"
msgstr "A"
"B"
`)
	c, err := Parse("po", raw)
	if err != nil {
		t.Fatal(err)
	}
	m := entriesMap(c)
	if len(c.Entries) != 3 || m["Hello"] != "" || m["menu\x04File"] != "Fichier" || m["Multiline"] != "AB" {
		t.Fatalf("unexpected entries: %+v", c.Entries)
	}
	out, err := c.Render(map[string]string{"Hello": "こんにちは \"quoted\"", "Multiline": "changed"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "# Translator comment") || !strings.Contains(s, "#: src/main.c:1") || !strings.Contains(s, "Content-Type") {
		t.Fatalf("header or comments lost:\n%s", s)
	}
	c2, err := Parse("po", out)
	if err != nil {
		t.Fatalf("re-parse: %v\n%s", err, s)
	}
	m2 := entriesMap(c2)
	if m2["Hello"] != "こんにちは \"quoted\"" || m2["Multiline"] != "changed" || m2["menu\x04File"] != "Fichier" {
		t.Fatalf("render lost values:\n%s", s)
	}
}

func TestPORejectsPlural(t *testing.T) {
	raw := "msgid \"a\"\nmsgid_plural \"as\"\nmsgstr[0] \"x\"\nmsgstr[1] \"y\"\n"
	if _, err := Parse("po", []byte(raw)); err == nil {
		t.Fatal("expected plural rejection")
	}
}

func TestAndroidRoundTrip(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="utf-8"?>
<resources>
    <!-- comment -->
    <string name="app_name" translatable="false">Konnyaku</string>
    <string name="hello">Hello &amp; welcome</string>
    <string name="bye">Bye</string>
</resources>
`)
	c, err := Parse("android", raw)
	if err != nil {
		t.Fatal(err)
	}
	m := entriesMap(c)
	if len(c.Entries) != 2 || m["hello"] != "Hello & welcome" || m["bye"] != "Bye" {
		t.Fatalf("unexpected entries: %+v", c.Entries)
	}
	out, err := c.Render(map[string]string{"hello": "<b> & こんにちは"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "<!-- comment -->") || !strings.Contains(s, `translatable="false">Konnyaku<`) || !strings.Contains(s, "&lt;b&gt; &amp; こんにちは") || !strings.Contains(s, `<string name="bye">Bye</string>`) {
		t.Fatalf("unexpected output:\n%s", s)
	}
	if _, err = Parse("android", out); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
}

func TestAndroidRejects(t *testing.T) {
	for _, raw := range []string{
		`<resources><string name="a">x</string><string name="a">y</string></resources>`,
		`<resources><plurals name="a"></plurals></resources>`,
		`<!DOCTYPE x><resources></resources>`,
		`<resources><string name="a">x<b>y</b></string></resources>`,
		`<other/>`,
	} {
		if _, err := Parse("android", []byte(raw)); err == nil {
			t.Errorf("expected rejection for %s", raw)
		}
	}
}

func TestSizeLimit(t *testing.T) {
	if _, err := Parse("json", make([]byte, MaxSize+1)); err == nil {
		t.Fatal("expected size rejection")
	}
	if _, err := Parse("csv", []byte("a,b")); err == nil {
		t.Fatal("expected unsupported format")
	}
}
