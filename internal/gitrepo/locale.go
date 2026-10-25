package gitrepo

import (
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// ParseLocaleName canonicalizes a locale spelled the way file systems spell it:
// "ja", "en_US", "zh-rCN" (Android), "pt-BR". It rejects names that are not
// languages ("index", "strings") and returns the BCP 47 form plus a display name.
func ParseLocaleName(raw string) (code, name string, ok bool) {
	v := strings.ReplaceAll(raw, "_", "-")
	// Android resource qualifiers: values-zh-rCN, values-b+sr+Latn.
	if strings.HasPrefix(v, "b+") {
		v = strings.ReplaceAll(strings.TrimPrefix(v, "b+"), "+", "-")
	} else if i := strings.Index(v, "-r"); i > 0 && len(v) == i+4 {
		v = v[:i] + "-" + v[i+2:]
	}
	if v == "" || len(v) > 64 {
		return "", "", false
	}
	tag, err := language.Parse(v)
	if err != nil || tag == language.Und {
		return "", "", false
	}
	name = display.English.Tags().Name(tag)
	if name == "" || strings.HasPrefix(name, "Unknown") {
		return "", "", false
	}
	return tag.String(), name, true
}

// LocaleFile is one translation file matched by a component's {locale} pattern.
type LocaleFile struct {
	Path    string // repository-relative path
	Raw     string // locale as spelled in the file name ("" for a default file such as values/strings.xml)
	Code    string // canonical BCP 47 code ("" for a default file or an unrecognized name)
	Default bool   // file without a locale qualifier (Android values/)
}

// Recognized reports whether the file maps to a locale (or is the default file).
func (f LocaleFile) Recognized() bool { return f.Default || f.Code != "" }

// LocaleFiles matches regular files in a pinned GitHub tree, including Android defaults.
func (s Snapshot) LocaleFiles(pattern string) ([]LocaleFile, error) {
	if err := ValidatePattern(pattern); err != nil {
		return nil, err
	}
	i := strings.Index(pattern, "{locale}")
	prefix, suffix := pattern[:i], pattern[i+len("{locale}"):]
	var out []LocaleFile
	for _, p := range s.Paths() {
		if (strings.HasSuffix(prefix, "-") || strings.HasSuffix(prefix, "_")) && p == strings.TrimRight(prefix, "-_")+suffix {
			out = append(out, LocaleFile{Path: p, Default: true})
			continue
		}
		if !strings.HasPrefix(p, prefix) || !strings.HasSuffix(p, suffix) || len(p) < len(prefix)+len(suffix) {
			continue
		}
		raw := p[len(prefix) : len(p)-len(suffix)]
		if strings.Contains(raw, "/") {
			continue
		}
		code, _, _ := ParseLocaleName(raw)
		out = append(out, LocaleFile{Path: p, Raw: raw, Code: code})
	}
	return out, nil
}

// FindLocaleFile picks the file for a locale: an exact canonical match first,
// then a file whose name is only the language ("ja" for "ja-JP") or a regional
// variant of a language-only request ("ja-JP" for "ja"), then the default file.
func FindLocaleFile(files []LocaleFile, locale string) *LocaleFile {
	want, err := language.Parse(locale)
	if err != nil {
		return nil
	}
	wantBase, _ := want.Base()
	_, _, wantRegion := want.Raw()
	var byBase *LocaleFile
	for i := range files {
		f := &files[i]
		if f.Default || f.Code == "" {
			continue
		}
		if f.Code == locale {
			return f
		}
		tag, err := language.Parse(f.Code)
		if err != nil {
			continue
		}
		base, _ := tag.Base()
		_, _, region := tag.Raw()
		if base != wantBase {
			continue
		}
		// Prefer the language-only file, then the first regional variant.
		if byBase == nil || (region.String() == "ZZ" && wantRegion.String() != "ZZ") {
			byBase = f
		}
	}
	if byBase != nil {
		return byBase
	}
	for i := range files {
		if files[i].Default {
			return &files[i]
		}
	}
	return nil
}

// LocalePath renders the pattern for a locale that has no file yet, following
// the spelling conventions of the existing files (underscores, Android -r).
func LocalePath(pattern, locale string, existing []LocaleFile) string {
	spelling := locale
	if strings.Contains(pattern, "values-{locale}") {
		if i := strings.IndexByte(locale, '-'); i > 0 && len(locale) == i+3 {
			spelling = locale[:i] + "-r" + locale[i+1:]
		}
	} else {
		for _, f := range existing {
			if strings.Contains(f.Raw, "_") {
				spelling = strings.ReplaceAll(locale, "-", "_")
				break
			}
		}
	}
	return strings.ReplaceAll(pattern, "{locale}", spelling)
}
