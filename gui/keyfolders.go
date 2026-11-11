package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// ---- settings (~/.config/fstabctl/settings.json, shared with the Python CLI) ----

// Settings are per user and travel in export bundles.
//   - KeyFolders: extra folders to show in Key folders, besides the standard ones
//     (for example "Claude Assistance").
//   - TrackedLinks: links found by Discover that you chose to keep in backups
//     and bundles, in addition to the links directly in your home folder.
type Settings struct {
	KeyFolders   []string `json:"key_folders"`
	TrackedLinks []string `json:"tracked_links"`
}

func settingsPath() string {
	return filepath.Join(homeDir(), ".config", "fstabctl", "settings.json")
}

func loadSettings() Settings {
	s := Settings{KeyFolders: []string{}, TrackedLinks: []string{}}
	if b, err := os.ReadFile(settingsPath()); err == nil {
		json.Unmarshal(b, &s)
	}
	if s.KeyFolders == nil {
		s.KeyFolders = []string{}
	}
	if s.TrackedLinks == nil {
		s.TrackedLinks = []string{}
	}
	return s
}

func saveSettings(s Settings) error {
	s.KeyFolders = uniqSorted(s.KeyFolders)
	s.TrackedLinks = uniqSorted(s.TrackedLinks)
	p := settingsPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	chownToUser(filepath.Dir(p), false)
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	chownToUser(p, false)
	return nil
}

func uniqSorted(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		v = strings.Trim(strings.TrimSpace(v), "/")
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// cleanName checks a folder or link name relative to home: no "..", not empty.
func cleanName(n string) (string, error) {
	n = strings.Trim(strings.TrimSpace(n), "/")
	if strings.HasPrefix(n, "~/") {
		n = n[2:]
	}
	c := filepath.Clean(n)
	if c == "." || c == "" || strings.HasPrefix(c, "..") || filepath.IsAbs(n) {
		return "", fail("'%s' isn't a folder name inside your home folder", n)
	}
	return c, nil
}

// ---- key folders --------------------------------------------------------

var standardFolders = []struct{ key, name string }{
	{"DOCUMENTS", "Documents"}, {"DOWNLOAD", "Downloads"}, {"MUSIC", "Music"},
	{"PICTURES", "Pictures"}, {"VIDEOS", "Videos"},
}

// standardFolderNames returns the five standard folders under the names your
// desktop uses (from ~/.config/user-dirs.dirs, so translated names work).
func standardFolderNames(home string) []string {
	xdg := map[string]string{}
	if b, err := os.ReadFile(filepath.Join(home, ".config", "user-dirs.dirs")); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			l = strings.TrimSpace(l)
			k, v, ok := strings.Cut(l, "=")
			if !ok || !strings.HasPrefix(k, "XDG_") || !strings.HasSuffix(k, "_DIR") {
				continue
			}
			v = strings.Trim(v, `"`)
			if rel, ok := strings.CutPrefix(v, "$HOME/"); ok && rel != "" {
				xdg[strings.TrimSuffix(strings.TrimPrefix(k, "XDG_"), "_DIR")] = rel
			}
		}
	}
	out := make([]string, 0, len(standardFolders))
	for _, f := range standardFolders {
		if n, ok := xdg[f.key]; ok {
			out = append(out, n)
		} else {
			out = append(out, f.name)
		}
	}
	return out
}

// KeyFolder is one row of the Key folders panel.
type KeyFolder struct {
	Name   string `json:"name"`
	Custom bool   `json:"custom"`
	State  string `json:"state"` // link, empty, missing, files, other
	Target string `json:"target,omitempty"`
	OK     bool   `json:"ok"`
	Note   string `json:"note,omitempty"`
	Items  int    `json:"items"`
}

func keyFolders(home string, ents []IdxEntry, mounted map[string]bool) []KeyFolder {
	var out []KeyFolder
	add := func(name string, custom bool) {
		for _, k := range out {
			if k.Name == name {
				return
			}
		}
		k := KeyFolder{Name: name, Custom: custom}
		p := filepath.Join(home, name)
		switch {
		case isSymlink(p):
			raw, _ := os.Readlink(p)
			k.State, k.Target, k.OK = "link", raw, pathExists(p)
			for _, l := range describeLinks(home, []string{p}, ents, mounted) {
				k.Note = l.Note
			}
		case !lexists(p):
			k.State = "missing"
		case isDir(p):
			n, _ := countEntries(p)
			k.Items = n
			k.State = "files"
			if n == 0 {
				k.State = "empty"
			}
			if mounted[p] {
				k.State, k.Note = "other", "a drive is mounted here"
			}
		default:
			k.State, k.Note = "other", "a file, not a folder"
		}
		out = append(out, k)
	}
	for _, n := range standardFolderNames(home) {
		add(n, false)
	}
	for _, n := range loadSettings().KeyFolders {
		add(n, true)
	}
	return out
}

// countEntries counts what is directly inside dir, hidden files included.
func countEntries(dir string) (int, error) {
	f, err := os.Open(dir)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	return len(names), err
}

func isEmptyDir(p string) bool {
	if !isDir(p) || isSymlink(p) {
		return false
	}
	n, err := countEntries(p)
	return err == nil && n == 0
}

// ---- links kept in backups ----------------------------------------------

// describeLinks turns symlink paths into LinkInfo rows (status and notes).
func describeLinks(home string, paths []string, ents []IdxEntry, mounted map[string]bool) []LinkInfo {
	infos := []LinkInfo{}
	for _, link := range paths {
		raw, err := os.Readlink(link)
		if err != nil {
			continue
		}
		tgt := linkAbsTarget(link, raw)
		ok := pathExists(link)
		e := fstabMountFor(tgt, ents)
		note := ""
		switch {
		case e != nil:
			up := mounted[e.File]
			switch {
			case ok && up:
				note = fmt.Sprintf("on %s (mounted)", e.File)
			case ok:
				note = fmt.Sprintf("on %s (NOT mounted)", e.File)
			case !up:
				note = fmt.Sprintf("%s is in fstab but not mounted (try: Mount now)", e.File)
			default:
				note = fmt.Sprintf("%s is mounted but this path is missing", e.File)
			}
		case !ok:
			note = "target path does not exist"
		}
		rel, _ := filepath.Rel(home, link)
		infos = append(infos, LinkInfo{Name: rel, Target: raw, AbsTarget: tgt, OK: ok, Note: note})
	}
	return infos
}

// managedLinks is what backups and bundles keep: the links directly in your
// home folder, plus links you chose to track (from Discover).
func managedLinks(home string, ents []IdxEntry, mounted map[string]bool) []LinkInfo {
	paths := scanLinks(home, false, 1)
	seen := map[string]bool{}
	for _, p := range paths {
		seen[p] = true
	}
	for _, n := range loadSettings().TrackedLinks {
		p := filepath.Join(home, n)
		if !seen[p] && isSymlink(p) {
			paths = append(paths, p)
			seen[p] = true
		}
	}
	sort.Strings(paths)
	infos := describeLinks(home, paths, ents, mounted)
	for i := range infos {
		infos[i].Tracked = trackedSet()[infos[i].Name]
	}
	return infos
}

func trackedSet() map[string]bool {
	m := map[string]bool{}
	for _, n := range loadSettings().TrackedLinks {
		m[n] = true
	}
	return m
}

// ---- discovery ------------------------------------------------------------

// Folders that hold app data, caches or toolchains: full of links apps made,
// slow to search, and not what anyone means by "my links".
var discoverSkip = map[string]bool{
	".cache": true, ".var": true, "snap": true, ".local/share/Trash": true, ".local/share/flatpak": true,
	".local/share/containers": true, ".local/share/Steam": true, ".steam": true, ".mozilla": true,
	".thunderbird": true, ".config/google-chrome": true, ".config/chromium": true, ".config/BraveSoftware": true,
	".config/Code": true, ".vscode": true, ".vscode-server": true, ".npm": true, ".nvm": true, ".cargo": true,
	".rustup": true, ".pyenv": true, ".gradle": true, ".m2": true, ".android": true, ".wine": true,
	".local/lib": true, ".local/share/virtualenvs": true, "go/pkg": true, ".docker": true,
}

var discoverSkipNames = map[string]bool{"node_modules": true, ".git": true, ".venv": true, "venv": true,
	"__pycache__": true, ".tox": true}

// DiscoveredLink is one link found by Discover.
type DiscoveredLink struct {
	LinkInfo
	Kept     bool   `json:"kept"`     // already in backups (top level or tracked)
	ByApp    bool   `json:"by_app"`   // probably made by an app, not by you
	Location string `json:"location"` // the folder it sits in, relative to home
}

// discoverLinks searches home for symlinks, up to maxDepth folders deep. It
// skips app-data folders, other mounted drives, and (unless hidden) hidden
// folders. It never follows links.
func discoverLinks(home string, hidden bool, maxDepth int, ents []IdxEntry, mounted map[string]bool) ([]DiscoveredLink, bool) {
	var st syscall.Stat_t
	homeDev := uint64(0)
	if syscall.Stat(home, &st) == nil {
		homeDev = uint64(st.Dev)
	}
	var paths []string
	visited, truncated := 0, false
	var walk func(dir string, lvl int)
	walk = func(dir string, lvl int) {
		if visited++; visited > 20000 || len(paths) >= 1000 {
			truncated = true
			return
		}
		items, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, it := range items {
			p := filepath.Join(dir, it.Name())
			rel, _ := filepath.Rel(home, p)
			if strings.HasPrefix(it.Name(), ".") && !hidden {
				continue
			}
			if it.Type()&os.ModeSymlink != 0 {
				paths = append(paths, p)
				continue
			}
			if !it.IsDir() || lvl >= maxDepth || discoverSkip[rel] || discoverSkipNames[it.Name()] {
				continue
			}
			var s syscall.Stat_t
			if syscall.Lstat(p, &s) == nil && homeDev != 0 && uint64(s.Dev) != homeDev {
				continue // another drive mounted inside home: don't search it
			}
			walk(p, lvl+1)
		}
	}
	walk(home, 1)
	sort.Strings(paths)
	kept := map[string]bool{}
	for _, l := range managedLinks(home, ents, mounted) {
		kept[l.Name] = true
	}
	var out []DiscoveredLink
	for _, l := range describeLinks(home, paths, ents, mounted) {
		d := DiscoveredLink{LinkInfo: l, Kept: kept[l.Name], Location: filepath.Dir(l.Name)}
		d.Tracked = trackedSet()[l.Name]
		d.ByApp = madeByApp(l)
		out = append(out, d)
	}
	return out, truncated
}

// madeByApp guesses whether a link was made by software rather than by hand:
// links inside hidden folders, or pointing at system or app locations.
func madeByApp(l LinkInfo) bool {
	for _, part := range strings.Split(l.Name, "/")[:strings.Count(l.Name, "/")] {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	for _, p := range []string{"/usr/", "/opt/", "/snap/", "/var/lib/", "/proc/", "/run/", "/etc/", "/nix/"} {
		if strings.HasPrefix(l.AbsTarget+"/", p) {
			return true
		}
	}
	return strings.HasPrefix(filepath.Base(l.Name), ".")
}

// ---- commands: link discover / track / untrack / folders ---------------

func (c *Ctx) linkDiscover(args []string) error {
	a, err := parseArgs(args, "hidden", "depth=")
	if err != nil {
		return err
	}
	depth := 6
	if a.has("depth") {
		fmt.Sscan(a.str("depth"), &depth)
	}
	depth = max(1, min(10, depth))
	home := homeDir()
	found, trunc := discoverLinks(home, a.has("hidden"), depth, currentEntriesQuiet(), mountedPoints())
	if len(found) == 0 {
		c.pl("No links found.")
		return nil
	}
	rows := [][]string{}
	for _, d := range found {
		status := "not kept"
		if d.Kept {
			status = "kept"
		}
		if d.ByApp {
			status += ", by an app?"
		}
		rows = append(rows, []string{"~/" + d.Name, d.Target, status})
	}
	c.pl(table([]string{"LINK", "TARGET", "IN BACKUPS"}, rows, false))
	if trunc {
		c.pl("\n(stopped early: the search limit was reached)")
	}
	c.pl("\nKeep a link in backups and bundles with: fstabctl link track NAME")
	return nil
}

func (c *Ctx) linkTrack(args []string, track bool) error {
	a, err := parseArgs(args)
	if err != nil {
		return err
	}
	if len(a.pos) == 0 {
		return fail("needs at least one link NAME (relative to your home folder)")
	}
	home := homeDir()
	s := loadSettings()
	cur := map[string]bool{}
	for _, n := range s.TrackedLinks {
		cur[n] = true
	}
	for _, raw := range a.pos {
		n, err := cleanName(raw)
		if err != nil {
			return err
		}
		if track {
			if !isSymlink(filepath.Join(home, n)) {
				return fail("~/%s is not a symlink", n)
			}
			cur[n] = true
			c.pf("  keep ~/%s in backups and bundles\n", n)
		} else {
			delete(cur, n)
			c.pf("  stop keeping ~/%s (the link itself is not touched)\n", n)
		}
	}
	if !c.apply {
		c.pl("\n(dry run: nothing changed)")
		return nil
	}
	s.TrackedLinks = s.TrackedLinks[:0]
	for n := range cur {
		s.TrackedLinks = append(s.TrackedLinks, n)
	}
	if err := saveSettings(s); err != nil {
		return err
	}
	c.pl("Saved.")
	return nil
}

func (c *Ctx) linkFolders(args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	home := homeDir()
	switch args[0] {
	case "list":
		rows := [][]string{}
		for _, k := range keyFolders(home, currentEntriesQuiet(), mountedPoints()) {
			state := map[string]string{"link": "link -> " + k.Target, "empty": "empty folder",
				"missing": "doesn't exist", "files": fmt.Sprintf("folder with %d item(s)", k.Items), "other": k.Note}[k.State]
			if k.State == "link" && !k.OK {
				state += " (BROKEN)"
			}
			kind := "standard"
			if k.Custom {
				kind = "yours"
			}
			rows = append(rows, []string{"~/" + k.Name, kind, state})
		}
		c.pl(table([]string{"FOLDER", "KIND", "STATE"}, rows, false))
		return nil
	case "add", "remove":
		if len(args) < 2 {
			return fail("folders %s needs a NAME", args[0])
		}
		s := loadSettings()
		for _, raw := range args[1:] {
			n, err := cleanName(raw)
			if err != nil {
				return err
			}
			if args[0] == "add" {
				s.KeyFolders = append(s.KeyFolders, n)
				c.pf("  add ~/%s to Key folders\n", n)
			} else {
				kept := s.KeyFolders[:0]
				for _, k := range s.KeyFolders {
					if k != n {
						kept = append(kept, k)
					}
				}
				s.KeyFolders = kept
				c.pf("  remove ~/%s from Key folders (the folder itself is not touched)\n", n)
			}
		}
		if !c.apply {
			c.pl("\n(dry run: nothing changed)")
			return nil
		}
		if err := saveSettings(s); err != nil {
			return err
		}
		c.pl("Saved.")
		return nil
	}
	return fail("unknown folders action '%s' (list, add, remove)", args[0])
}
