// Package gitrepo accesses GitHub repositories through the REST API without local checkouts.
package gitrepo

import (
	"errors"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func GitHubRepository(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return "", errors.New("repository must be https://github.com/owner/repo.git")
	}
	repo := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".git")
	if !repoName.MatchString(repo) {
		return "", errors.New("invalid GitHub repository")
	}
	for _, part := range strings.Split(repo, "/") {
		if part == "." || part == ".." {
			return "", errors.New("invalid GitHub repository")
		}
	}
	return repo, nil
}
func ValidateBranch(v string) error {
	if v == "" || len(v) > 200 || strings.HasPrefix(v, "-") || strings.HasPrefix(v, "/") || strings.HasSuffix(v, "/") || strings.Contains(v, "..") || strings.Contains(v, "@{") || strings.ContainsAny(v, " ~^:?*[\\\r\n\x00") || v == "@" {
		return errors.New("invalid branch")
	}
	for _, p := range strings.Split(v, "/") {
		if p == "" || strings.HasPrefix(p, ".") || strings.HasSuffix(p, ".") || strings.HasSuffix(p, ".lock") {
			return errors.New("invalid branch")
		}
	}
	return nil
}
func ValidatePattern(v string) error {
	if strings.Count(v, "{locale}") != 1 {
		return errors.New("file pattern must contain {locale} exactly once")
	}
	return validPath(strings.ReplaceAll(v, "{locale}", "en"))
}
func validPath(v string) error {
	if !filepath.IsLocal(v) || strings.ContainsAny(v, "\\\x00\r\n:*?[]{}") || len(v) > 512 {
		return errors.New("invalid repository path")
	}
	for _, part := range strings.Split(v, "/") {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return errors.New("unsafe repository path")
		}
	}
	return nil
}

type Candidate struct {
	Pattern string   `json:"pattern"`
	Format  string   `json:"format"`
	Locales []string `json:"locales"`
}

// Scan finds directories whose files (or sub-directories) are named after locale
// codes and suggests {locale} file patterns for them. Locales are reported in
// canonical form; a values/strings.xml default file counts for Android layouts.
func (s Snapshot) Scan() ([]Candidate, error) {
	isLocale := func(v string) bool { _, _, ok := ParseLocaleName(v); return ok }
	found := map[string]*Candidate{}
	formats := map[string]string{".json": "json", ".yaml": "yaml", ".yml": "yaml", ".po": "po", ".xml": "android"}
	skip := map[string]bool{".git": true, "node_modules": true, "vendor": true, "build": true, "dist": true, "target": true}
	for _, rel := range s.Paths() {
		ignored := false
		parts := strings.Split(rel, "/")
		for _, part := range parts[:len(parts)-1] {
			if skip[part] {
				ignored = true
			}
		}
		if ignored || len(parts) > 9 {
			continue
		}
		ext := strings.ToLower(filepath.Ext(rel))
		format, ok := formats[ext]
		if !ok {
			continue
		}
		rel = filepath.ToSlash(rel)
		dir, file := pathSplit(rel)
		base := strings.TrimSuffix(file, filepath.Ext(file))
		var pattern, locale string
		if isLocale(base) {
			pattern, locale = joinPattern(dir, "{locale}"+ext), base
		} else if parent, name := pathSplit(dir); name != "" && isLocale(name) {
			pattern, locale = joinPattern(parent, "{locale}/"+file), name
		} else if format == "android" && strings.HasPrefix(name, "values-") && isLocale(strings.TrimPrefix(name, "values-")) && file == "strings.xml" {
			pattern, locale = joinPattern(parent, "values-{locale}/strings.xml"), strings.TrimPrefix(name, "values-")
		} else {
			continue
		}
		if ValidatePattern(pattern) != nil {
			continue
		}
		locale, _, _ = ParseLocaleName(locale)
		c := found[pattern]
		if c == nil {
			c = &Candidate{Pattern: pattern, Format: format}
			found[pattern] = c
		}
		duplicate := false
		for _, l := range c.Locales {
			if l == locale {
				duplicate = true
			}
		}
		if duplicate {
			continue
		}
		c.Locales = append(c.Locales, locale)

	}
	out := make([]Candidate, 0, len(found))
	for _, c := range found {
		sort.Strings(c.Locales)
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pattern < out[j].Pattern })
	return out, nil
}
func pathSplit(p string) (dir, name string) {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i], p[i+1:]
	}
	return "", p
}
func joinPattern(dir, tail string) string {
	if dir == "" {
		return tail
	}
	return dir + "/" + tail
}
