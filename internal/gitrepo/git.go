// Package gitrepo confines all Git work to managed checkouts. Only GitHub HTTPS
// remotes are accepted in this initial version; no shell or submodules are used.
package gitrepo

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
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

type Repository struct {
	Root               string
	ID                 int64
	URL, Branch, Token string
}

func (r Repository) Dir() string { return filepath.Join(r.Root, strconv.FormatInt(r.ID, 10)) }
func (r Repository) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "http.followRedirects=false", "-c", "credential.helper=", "-c", "commit.gpgSign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent", "LANG=C.UTF-8", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_LITERAL_PATHSPECS=1", "GIT_AUTHOR_NAME=Konnyaku", "GIT_AUTHOR_EMAIL=konnyaku@localhost", "GIT_COMMITTER_NAME=Konnyaku", "GIT_COMMITTER_EMAIL=konnyaku@localhost"}
	if r.Token != "" {
		cmd.Env = append(cmd.Env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://github.com/.extraheader", "GIT_CONFIG_VALUE_0=Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte("x-access-token:"+r.Token)))
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s failed (check branch, credentials, and worktree conflicts): %w", args[0], err)
	}
	return out, nil
}
func (r Repository) Clone(ctx context.Context) error {
	if _, err := GitHubRepository(r.URL); err != nil {
		return err
	}
	if err := ValidateBranch(r.Branch); err != nil {
		return err
	}
	if err := os.MkdirAll(r.Root, 0700); err != nil {
		return err
	}
	if _, err := os.Lstat(r.Dir()); err == nil {
		return errors.New("checkout already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.MkdirTemp(r.Root, "clone-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if _, err = r.run(ctx, "", "clone", "--no-local", "--single-branch", "--branch", r.Branch, "--", r.URL, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, r.Dir())
}
func (r Repository) Pull(ctx context.Context) error {
	if _, err := GitHubRepository(r.URL); err != nil {
		return err
	}
	if err := ValidateBranch(r.Branch); err != nil {
		return err
	}
	if err := r.Clean(ctx); err != nil {
		return err
	}
	if _, err := r.run(ctx, r.Dir(), "checkout", r.Branch); err != nil {
		return err
	}
	_, err := r.run(ctx, r.Dir(), "pull", "--ff-only", "--no-rebase", r.URL, r.Branch)
	return err
}
func (r Repository) Clean(ctx context.Context) error {
	out, err := r.run(ctx, r.Dir(), "status", "--porcelain")
	if err != nil {
		return err
	}
	if len(out) != 0 {
		return errors.New("worktree has uncommitted changes")
	}
	return nil
}
func (r Repository) Push(ctx context.Context) error {
	if _, err := GitHubRepository(r.URL); err != nil {
		return err
	}
	if err := ValidateBranch(r.Branch); err != nil {
		return err
	}
	_, err := r.run(ctx, r.Dir(), "push", r.URL, "HEAD:refs/heads/"+r.Branch)
	return err
}
func (r Repository) Commit(ctx context.Context, path, message string) error {
	if err := validPath(path); err != nil {
		return err
	}
	if strings.TrimSpace(message) == "" || len(message) > 500 {
		return errors.New("commit message required (up to 500 bytes)")
	}
	if _, err := r.run(ctx, r.Dir(), "add", "--", path); err != nil {
		return err
	}
	_, err := r.run(ctx, r.Dir(), "commit", "--only", "-m", message, "--", path)
	return err
}
func (r Repository) SafePath(path string) (string, error) {
	if err := validPath(path); err != nil {
		return "", err
	}
	current := r.Dir()
	info, err := os.Lstat(current)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("invalid checkout directory")
	}
	for _, p := range strings.Split(path, "/") {
		current = filepath.Join(current, p)
		info, err = os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("symlinks are not allowed for translation paths")
		}
	}
	return current, nil
}
func (r Repository) Read(path string) ([]byte, error) {
	p, err := r.SafePath(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular translation file")
	}
	return io.ReadAll(io.LimitReader(f, 4<<20+1))
}
func (r Repository) Write(path string, data []byte) error {
	p, err := r.SafePath(path)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".konnyaku-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}

// CommitPaths stages the given paths and commits them. It reports ErrNothingToCommit
// when the paths carry no changes.
var ErrNothingToCommit = errors.New("nothing to commit")

func (r Repository) CommitPaths(ctx context.Context, paths []string, message string) error {
	if len(paths) == 0 {
		return ErrNothingToCommit
	}
	for _, p := range paths {
		if err := validPath(p); err != nil {
			return err
		}
	}
	if strings.TrimSpace(message) == "" || len(message) > 500 {
		return errors.New("commit message required (up to 500 bytes)")
	}
	if _, err := r.run(ctx, r.Dir(), append([]string{"add", "--"}, paths...)...); err != nil {
		return err
	}
	out, err := r.run(ctx, r.Dir(), append([]string{"status", "--porcelain", "--"}, paths...)...)
	if err != nil {
		return err
	}
	if len(out) == 0 {
		return ErrNothingToCommit
	}
	_, err = r.run(ctx, r.Dir(), append([]string{"commit", "--only", "-m", message, "--"}, paths...)...)
	return err
}

// Checkout switches to an existing local or remote-tracking branch; when create is
// set a new branch is started from the current HEAD.
func (r Repository) Checkout(ctx context.Context, branch string, create bool) error {
	if err := ValidateBranch(branch); err != nil {
		return err
	}
	args := []string{"checkout"}
	if create {
		args = append(args, "-b")
	}
	_, err := r.run(ctx, r.Dir(), append(args, branch, "--")...)
	return err
}

// PushBranch pushes the current HEAD to the named remote branch.
func (r Repository) PushBranch(ctx context.Context, branch string) error {
	if _, err := GitHubRepository(r.URL); err != nil {
		return err
	}
	if err := ValidateBranch(branch); err != nil {
		return err
	}
	_, err := r.run(ctx, r.Dir(), "push", r.URL, "HEAD:refs/heads/"+branch)
	return err
}

type Status struct {
	Exists   bool   `json:"exists"`
	Branch   string `json:"branch"`
	Commit   string `json:"commit"`
	Subject  string `json:"subject"`
	Dirty    bool   `json:"dirty"`
	Modified int    `json:"modified"`
}

// Status describes the checkout without touching the network.
func (r Repository) Status(ctx context.Context) (Status, error) {
	var s Status
	if _, err := os.Lstat(r.Dir()); errors.Is(err, os.ErrNotExist) {
		return s, nil
	} else if err != nil {
		return s, err
	}
	s.Exists = true
	out, err := r.run(ctx, r.Dir(), "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return s, err
	}
	s.Branch = strings.TrimSpace(string(out))
	out, err = r.run(ctx, r.Dir(), "log", "-1", "--format=%h%x00%s")
	if err != nil {
		return s, err
	}
	if parts := strings.SplitN(strings.TrimSpace(string(out)), "\x00", 2); len(parts) == 2 {
		s.Commit, s.Subject = parts[0], parts[1]
	}
	out, err = r.run(ctx, r.Dir(), "status", "--porcelain")
	if err != nil {
		return s, err
	}
	s.Modified = strings.Count(string(out), "\n")
	s.Dirty = s.Modified > 0
	return s, nil
}

// Candidate is a translation file layout discovered in the checkout.
type Candidate struct {
	Pattern string   `json:"pattern"`
	Format  string   `json:"format"`
	Locales []string `json:"locales"`
}

// Scan finds directories whose files (or sub-directories) are named after locale
// codes and suggests {locale} file patterns for them. Locales are reported in
// canonical form; a values/strings.xml default file counts for Android layouts.
func (r Repository) Scan() ([]Candidate, error) {
	isLocale := func(v string) bool { _, _, ok := ParseLocaleName(v); return ok }
	root, err := filepath.Abs(r.Dir())
	if err != nil {
		return nil, err
	}
	found := map[string]*Candidate{}
	formats := map[string]string{".json": "json", ".yaml": "yaml", ".yml": "yaml", ".po": "po", ".xml": "android"}
	skip := map[string]bool{".git": true, "node_modules": true, "vendor": true, "build": true, "dist": true, "target": true}
	count := 0
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if rel != "." && (skip[d.Name()] || strings.Count(rel, string(filepath.Separator)) >= 8) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if count++; count > 50000 {
			return errors.New("repository too large to scan")
		}
		ext := strings.ToLower(filepath.Ext(rel))
		format, ok := formats[ext]
		if !ok {
			return nil
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
			return nil
		}
		if ValidatePattern(pattern) != nil {
			return nil
		}
		locale, _, _ = ParseLocaleName(locale)
		c := found[pattern]
		if c == nil {
			c = &Candidate{Pattern: pattern, Format: format}
			found[pattern] = c
		}
		for _, l := range c.Locales {
			if l == locale {
				return nil
			}
		}
		c.Locales = append(c.Locales, locale)
		return nil
	})
	if err != nil {
		return nil, err
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
