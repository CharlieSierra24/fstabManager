package main

import (
	"fmt"
	"strconv"
	"strings"
)

const disabledMark = "#fstabctl-disabled#"

var systemMounts = map[string]bool{"/": true, "/boot": true, "/boot/efi": true, "/efi": true}

// Entry is one fstab line. JSON tags match the bundle format of the Python CLI.
type Entry struct {
	Spec    string `json:"spec"`
	File    string `json:"file"`
	Fstype  string `json:"fstype"`
	Options string `json:"options"`
	Dump    int    `json:"dump"`
	Pass    int    `json:"passno"`
	Enabled bool   `json:"enabled"`
}

func (e *Entry) Line() string {
	body := fmt.Sprintf("%-42s %-22s %-8s %-44s %d %d", escape(e.Spec), escape(e.File), e.Fstype, e.Options, e.Dump, e.Pass)
	if !e.Enabled {
		return disabledMark + " " + body
	}
	return body
}

func (e *Entry) Opts() []string {
	var out []string
	for _, o := range strings.Split(e.Options, ",") {
		if o != "" {
			out = append(out, o)
		}
	}
	return out
}

func (e *Entry) HasOpt(name string) bool {
	for _, o := range e.Opts() {
		if o == name {
			return true
		}
	}
	return false
}

// parseLine returns (nil, nil) for blank/comment lines.
func parseLine(raw string) (*Entry, error) {
	s := strings.TrimSpace(raw)
	enabled := true
	if strings.HasPrefix(s, disabledMark) {
		s = strings.TrimSpace(strings.TrimPrefix(s, disabledMark))
		enabled = false
	} else if s == "" || strings.HasPrefix(s, "#") {
		return nil, nil
	}
	p := strings.Fields(s)
	if len(p) < 4 || len(p) > 6 {
		return nil, fmt.Errorf("expected 4-6 fields, found %d", len(p))
	}
	e := &Entry{Spec: unescape(trimQ(p[0])), File: unescape(trimQ(p[1])), Fstype: trimQ(p[2]), Options: trimQ(p[3]), Enabled: enabled}
	var err error
	if len(p) > 4 {
		if e.Dump, err = strconv.Atoi(p[4]); err != nil {
			return nil, fmt.Errorf("dump/pass fields must be integers")
		}
	}
	if len(p) > 5 {
		if e.Pass, err = strconv.Atoi(p[5]); err != nil {
			return nil, fmt.Errorf("dump/pass fields must be integers")
		}
	}
	return e, nil
}

type IdxEntry struct {
	Idx int
	E   *Entry
}

type Fstab struct{ Lines []string }

func parseFstab(text string) *Fstab {
	if text == "" {
		return &Fstab{}
	}
	text = strings.TrimSuffix(text, "\n")
	return &Fstab{Lines: strings.Split(text, "\n")}
}

func (f *Fstab) Entries() []IdxEntry {
	var out []IdxEntry
	for i, raw := range f.Lines {
		e, err := parseLine(raw)
		if err != nil || e == nil {
			continue
		}
		out = append(out, IdxEntry{i, e})
	}
	return out
}

func (f *Fstab) Problems() []string {
	var out []string
	for i, raw := range f.Lines {
		if _, err := parseLine(raw); err != nil {
			out = append(out, fmt.Sprintf("line %d: %v: %s", i+1, err, strings.TrimSpace(raw)))
		}
	}
	return out
}

func (f *Fstab) Text() string            { return strings.Join(f.Lines, "\n") + "\n" }
func (f *Fstab) Append(e *Entry)         { f.Lines = append(f.Lines, e.Line()) }
func (f *Fstab) Replace(i int, e *Entry) { f.Lines[i] = e.Line() }
func (f *Fstab) Remove(i int)            { f.Lines = append(f.Lines[:i], f.Lines[i+1:]...) }

func readFstab() (string, error) {
	b, err := readFile(fstabPath())
	if err != nil {
		if isNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("cannot read %s: %v", fstabPath(), err)
	}
	return string(b), nil
}

// validate returns blocking problems found in an fstab text.
func validate(text string) []string {
	f := parseFstab(text)
	errs := f.Problems()
	seen := map[string]string{}
	for _, ie := range f.Entries() {
		e := ie.E
		if !e.Enabled {
			continue
		}
		if !strings.HasPrefix(e.File, "/") && e.File != "none" && e.File != "swap" {
			errs = append(errs, fmt.Sprintf("%s: mount point '%s' must be an absolute path", e.Spec, e.File))
		}
		if e.Fstype != "swap" && e.File != "none" {
			if prev, ok := seen[e.File]; ok {
				errs = append(errs, fmt.Sprintf("duplicate mount point %s (%s and %s)", e.File, prev, e.Spec))
			}
			seen[e.File] = e.Spec
		}
	}
	return errs
}

func findTarget(f *Fstab, target string) (IdxEntry, error) {
	ents := f.Entries()
	if n, err := strconv.Atoi(target); err == nil && n >= 1 && n <= len(ents) {
		return ents[n-1], nil
	}
	var hits []IdxEntry
	for _, ie := range ents {
		if ie.E.File == target || ie.E.Spec == target {
			hits = append(hits, ie)
		}
	}
	if len(hits) == 0 {
		return IdxEntry{}, fmt.Errorf("no entry matches '%s' (use a mount point, device spec, or # from 'list')", target)
	}
	if len(hits) > 1 {
		return IdxEntry{}, fmt.Errorf("'%s' matches several entries; use the # from the entry list", target)
	}
	return hits[0], nil
}

// trimQ removes surrounding quotes, as in UUID="abcd-1234" (the quotes sit
// around the value after the "=") or a bare "value" field.
func trimQ(s string) string {
	if i := strings.Index(s, "="); i > 0 && i+1 < len(s) {
		if v := s[i+1:]; len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			return s[:i+1] + v[1:len(v)-1]
		}
		return s
	}
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}
