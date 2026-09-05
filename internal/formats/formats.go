// Package formats implements deliberately strict, loss-aware translation catalogs.
// Unsupported constructs are rejected instead of silently dropping translations.
package formats

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

const MaxSize = 4 << 20

type Entry struct {
	Key   string
	Value string
}
type Catalog struct {
	Entries []Entry
	render  func(map[string]string) ([]byte, error)
}

func (c *Catalog) Render(values map[string]string) ([]byte, error) { return c.render(values) }

func Parse(format string, raw []byte) (*Catalog, error) {
	if len(raw) > MaxSize {
		return nil, errors.New("file exceeds 4 MiB")
	}
	switch format {
	case "json", "yaml":
		return parseTree(format, raw)
	case "po":
		return parsePO(raw)
	case "android":
		return parseAndroid(raw)
	}
	return nil, errors.New("unsupported format")
}

// JSON and YAML share a node tree; mappings may nest, leaves must be strings.
// Keys are JSON pointers, so literal dots/slashes cannot collide with nesting.
func parseTree(format string, raw []byte) (*Catalog, error) {
	if format == "json" && !json.Valid(raw) {
		return nil, errors.New("invalid JSON")
	}
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("expected one document")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("expected a mapping")
	}
	c := &Catalog{}
	nodes := map[string]*yaml.Node{}
	var walk func(*yaml.Node, string, int) error
	walk = func(n *yaml.Node, path string, depth int) error {
		if depth > 32 {
			return errors.New("nesting exceeds 32 levels")
		}
		if n.Kind == yaml.MappingNode {
			seen := map[string]bool{}
			for i := 0; i < len(n.Content); i += 2 {
				k := n.Content[i]
				if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || seen[k.Value] {
					return errors.New("keys must be unique strings")
				}
				seen[k.Value] = true
				part := strings.ReplaceAll(strings.ReplaceAll(k.Value, "~", "~0"), "/", "~1")
				if err := walk(n.Content[i+1], path+"/"+part, depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
			return fmt.Errorf("%s: only string leaves supported (no arrays, aliases, or numbers)", path)
		}
		nodes[path] = n
		c.Entries = append(c.Entries, Entry{path, n.Value})
		return nil
	}
	if err := walk(doc.Content[0], "", 0); err != nil {
		return nil, err
	}
	c.render = func(values map[string]string) ([]byte, error) {
		for k, n := range nodes {
			if v, ok := values[k]; ok {
				n.Value = v
			}
		}
		if format == "yaml" {
			return yaml.Marshal(&doc)
		}
		var toMap func(*yaml.Node) any
		toMap = func(n *yaml.Node) any {
			if n.Kind == yaml.MappingNode {
				m := map[string]any{}
				for i := 0; i < len(n.Content); i += 2 {
					m[n.Content[i].Value] = toMap(n.Content[i+1])
				}
				return m
			}
			return n.Value
		}
		b, err := json.MarshalIndent(toMap(doc.Content[0]), "", "  ")
		return append(b, '\n'), err
	}
	return c, nil
}

type span struct {
	start, end int
	key        string
}

func patch(raw []byte, spans []span, values map[string]string, encode func(string) string) []byte {
	var out bytes.Buffer
	pos := 0
	for _, s := range spans {
		out.Write(raw[pos:s.start])
		if v, ok := values[s.key]; ok {
			out.WriteString(encode(v))
		} else {
			out.Write(raw[s.start:s.end])
		}
		pos = s.end
	}
	out.Write(raw[pos:])
	return out.Bytes()
}
func parseAndroid(raw []byte) (*Catalog, error) {
	d := xml.NewDecoder(bytes.NewReader(raw))
	c := &Catalog{}
	var spans []span
	seen := map[string]bool{}
	depth := 0
	root := false
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.Directive:
			return nil, errors.New("XML directives are unsupported")
		case xml.StartElement:
			depth++
			if depth == 1 {
				if root || t.Name.Local != "resources" || t.Name.Space != "" {
					return nil, errors.New("expected resources root")
				}
				root = true
				continue
			}
			if depth != 2 || t.Name.Local != "string" || t.Name.Space != "" {
				return nil, errors.New("only plain Android string elements supported")
			}
			key := ""
			skip := false
			for _, a := range t.Attr {
				if a.Name.Local == "name" {
					key = a.Value
				}
				if a.Name.Local == "translatable" && a.Value == "false" {
					skip = true
				}
			}
			if key == "" || seen[key] {
				return nil, errors.New("missing or duplicate string name")
			}
			seen[key] = true
			start := int(d.InputOffset())
			var value strings.Builder
			end := start
			for {
				before := int(d.InputOffset())
				inner, e := d.Token()
				if e != nil {
					return nil, e
				}
				switch v := inner.(type) {
				case xml.CharData:
					value.Write(v)
				case xml.EndElement:
					end = before
					goto finished
				default:
					return nil, errors.New("styled Android strings are not yet supported")
				}
			}
		finished:
			depth--
			if !skip {
				c.Entries = append(c.Entries, Entry{key, value.String()})
				spans = append(spans, span{start, end, key})
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return nil, errors.New("text outside string")
			}
		}
	}
	if !root || depth != 0 {
		return nil, errors.New("invalid resources document")
	}
	c.render = func(v map[string]string) ([]byte, error) {
		return patch(raw, spans, v, func(s string) string { var b bytes.Buffer; _ = xml.EscapeText(&b, []byte(s)); return b.String() }), nil
	}
	return c, nil
}

func parsePO(raw []byte) (*Catalog, error) {
	// Preserve comments, headers, ordering, and formatting outside msgstr spans.
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	raw = []byte(text)
	lines := strings.SplitAfter(text, "\n")
	c := &Catalog{}
	var spans []span
	seen := map[string]bool{}
	offset := 0
	ctx, id, value, field := "", "", "", ""
	hasID, hasStr := false, false
	start, end := 0, 0
	flush := func() error {
		if !hasID && !hasStr && ctx == "" {
			return nil
		}
		if !hasID || !hasStr {
			return errors.New("PO entry needs msgid and msgstr")
		}
		if id != "" {
			key := id
			if ctx != "" {
				key = ctx + "\x04" + id
			}
			if seen[key] {
				return errors.New("duplicate PO key")
			}
			seen[key] = true
			c.Entries = append(c.Entries, Entry{key, value})
			spans = append(spans, span{start, end, key})
		}
		ctx, id, value, field = "", "", "", ""
		hasID, hasStr = false, false
		return nil
	}
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if s == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			offset += len(line)
			continue
		}
		if strings.HasPrefix(s, "#") {
			if hasStr {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			offset += len(line)
			continue
		}
		if strings.HasPrefix(s, "msgid_plural") || strings.HasPrefix(s, "msgstr[") {
			return nil, errors.New("plural PO entries are not yet supported")
		}
		var quoted string
		switch {
		case strings.HasPrefix(s, "msgctxt "):
			if hasID {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			field = "ctx"
			quoted = strings.TrimSpace(strings.TrimPrefix(s, "msgctxt"))
		case strings.HasPrefix(s, "msgid "):
			if hasID {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			hasID = true
			field = "id"
			quoted = strings.TrimSpace(strings.TrimPrefix(s, "msgid"))
		case strings.HasPrefix(s, "msgstr "):
			if hasStr {
				return nil, errors.New("duplicate msgstr")
			}
			hasStr = true
			field = "str"
			quoted = strings.TrimSpace(strings.TrimPrefix(s, "msgstr"))
			start = offset
			end = offset + len(line)
		case strings.HasPrefix(s, "\""):
			quoted = s
			if field == "str" {
				end = offset + len(line)
			}
		default:
			return nil, fmt.Errorf("unsupported PO syntax: %.40s", s)
		}
		v, err := strconv.Unquote(quoted)
		if err != nil {
			return nil, err
		}
		switch field {
		case "ctx":
			ctx += v
		case "id":
			id += v
		case "str":
			value += v
		default:
			return nil, errors.New("orphan PO continuation")
		}
		offset += len(line)
	}
	if err := flush(); err != nil {
		return nil, err
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	c.render = func(v map[string]string) ([]byte, error) {
		return patch(raw, spans, v, func(s string) string { return "msgstr " + strconv.Quote(s) + "\n" }), nil
	}
	return c, nil
}
