package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
)

type LinkInfo struct {
	Name      string `json:"name"`
	Target    string `json:"target"`
	AbsTarget string `json:"abs_target"`
	OK        bool   `json:"ok"`
	Note      string `json:"note"`
	Tracked   bool   `json:"tracked"`
}

// homeDir is the invoking user's home. FSTABCTL_HOME overrides it (used by tests).
func homeDir() string {
	if h := os.Getenv("FSTABCTL_HOME"); h != "" {
		return filepath.Clean(h)
	}
	return realUser().HomeDir
}

func fstabMountFor(path string, ents []IdxEntry) *Entry {
	var best *Entry
	for _, ie := range ents {
		e := ie.E
		if !e.Enabled || !strings.HasPrefix(e.File, "/") {
			continue
		}
		mp := strings.TrimRight(e.File, "/")
		if mp == "" {
			continue
		}
		if path == mp || strings.HasPrefix(path, mp+"/") {
			if best == nil || len(mp) > len(strings.TrimRight(best.File, "/")) {
				best = e
			}
		}
	}
	return best
}

func linkAbsTarget(link, raw string) string {
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(link), raw))
}

func linkPath(home, name string) (string, error) {
	p := filepath.Clean(filepath.Join(home, name))
	if filepath.IsAbs(name) {
		p = filepath.Clean(name)
	}
	if p == home || !strings.HasPrefix(p, strings.TrimRight(home, "/")+"/") {
		return "", fail("link name '%s' must be inside %s", name, home)
	}
	return p, nil
}

func scanLinks(home string, hidden bool, depth int) []string {
	var out []string
	var walk func(dir string, lvl int)
	walk = func(dir string, lvl int) {
		items, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, it := range items {
			if strings.HasPrefix(it.Name(), ".") && !hidden {
				continue
			}
			p := filepath.Join(dir, it.Name())
			if it.Type()&os.ModeSymlink != 0 {
				out = append(out, p)
			} else if lvl < depth && it.IsDir() {
				walk(p, lvl+1)
			}
		}
	}
	walk(home, 1)
	return out
}

func currentLinks(home string, hidden bool, depth int, ents []IdxEntry, mounted map[string]bool) []LinkInfo {
	infos := describeLinks(home, scanLinks(home, hidden, depth), ents, mounted)
	tr := trackedSet()
	for i := range infos {
		infos[i].Tracked = tr[infos[i].Name]
	}
	return infos
}

func makeSymlink(src, link string) error {
	dir := filepath.Dir(link)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".fstabctl-link-%d", os.Getpid()))
	os.Remove(tmp)
	if err := os.Symlink(src, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		os.Remove(tmp)
		return err
	}
	chownToUser(link, true)
	return nil
}

// moveTree renames, falling back to cp -a + remove when crossing filesystems.
func moveTree(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if _, e, code := runCmd("cp", "-a", "--", src, dst); code != 0 {
		os.RemoveAll(dst)
		return fmt.Errorf("copy failed (source left untouched): %s", strings.TrimSpace(e))
	}
	return os.RemoveAll(src)
}

// ---- snapshots (links-<id>.json, same format as the Python CLI) ------

type LinkSnap struct {
	ID      string    `json:"id"`
	Created string    `json:"created"`
	Host    string    `json:"host"`
	Home    string    `json:"home"`
	Note    string    `json:"note"`
	Sha256  string    `json:"sha256"`
	Links   []LinkRef `json:"links"`
	File    string    `json:"file,omitempty"`
}

func listLinkSnaps() []LinkSnap {
	var out []LinkSnap
	for _, p := range globSorted(filepath.Join(backupDir(), "links-*.json")) {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var s LinkSnap
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		s.File = p
		out = append(out, s)
	}
	return out
}

func jsonASCII(s string) string {
	b, _ := json.Marshal(s)
	var sb strings.Builder
	for _, r := range string(b) {
		if r < 128 {
			sb.WriteRune(r)
			continue
		}
		if r > 0xFFFF {
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(&sb, `\u%04x\u%04x`, r1, r2)
		} else {
			fmt.Fprintf(&sb, `\u%04x`, r)
		}
	}
	return sb.String()
}

func linksHash(refs []LinkRef) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = fmt.Sprintf(`{"name": %s, "target": %s}`, jsonASCII(r.Name), jsonASCII(r.Target))
	}
	return sha("[" + strings.Join(parts, ", ") + "]")
}

func saveLinkSnap(home string, links []LinkInfo, note string, dedupe bool) (LinkSnap, error) {
	refs := make([]LinkRef, len(links))
	for i, l := range links {
		refs[i] = LinkRef{l.Name, l.Target}
	}
	h := linksHash(refs)
	if dedupe {
		if s := listLinkSnaps(); len(s) > 0 && s[0].Sha256 == h && s[0].Home == home {
			return s[0], nil
		}
	}
	d, err := ensureBackupDir()
	if err != nil {
		return LinkSnap{}, err
	}
	id := uniqueID(d, "links-", ".json")
	snap := LinkSnap{ID: id, Created: time.Now().Format("2006-01-02T15:04:05"), Host: hostname(), Home: home,
		Note: note, Sha256: h, Links: refs}
	b, _ := json.MarshalIndent(snap, "", "  ")
	p := filepath.Join(d, "links-"+id+".json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		return LinkSnap{}, err
	}
	chownToUser(p, false)
	snap.File = p
	return snap, nil
}

func (c *Ctx) snapBefore(home string, names []string, note string) {
	depth := 1
	for _, n := range names {
		if d := strings.Count(n, "/") + 1; d > depth {
			depth = d
		}
	}
	ents := currentEntriesQuiet()
	mounted := mountedPoints()
	links := managedLinks(home, ents, mounted)
	have := map[string]bool{}
	for _, l := range links {
		have[l.Name] = true
	}
	for _, l := range currentLinks(home, false, depth, ents, mounted) {
		if !have[l.Name] {
			links = append(links, l)
		}
	}
	saveLinkSnap(home, links, "auto: before "+note, true)
}

func currentEntriesQuiet() []IdxEntry {
	t, _ := readFstab()
	return parseFstab(t).Entries()
}

// ---- commands ---------------------------------------------------------

func (c *Ctx) cmdLink(args []string) error {
	if len(args) == 0 {
		return fail("link needs an action")
	}
	rest := args[1:]
	switch args[0] {
	case "add":
		a, err := parseArgs(rest, "adopt", "force", "relative", "mkdir-target", "allow-missing", "replace-empty")
		if err != nil {
			return err
		}
		if len(a.pos) < 1 || len(a.pos) > 2 {
			return fail("link add needs TARGET [NAME]")
		}
		o := linkAddOpts{target: a.pos[0], adopt: a.has("adopt"), force: a.has("force"), relative: a.has("relative"),
			mkdirTarget: a.has("mkdir-target"), allowMissing: a.has("allow-missing"), replaceEmpty: a.has("replace-empty")}
		if len(a.pos) == 2 {
			o.name = a.pos[1]
		}
		return c.doLinkAdd(o)
	case "set":
		a, err := parseArgs(rest, "relative", "allow-missing")
		if err != nil {
			return err
		}
		if len(a.pos) != 2 {
			return fail("link set needs NAME TARGET")
		}
		link, err := linkPath(homeDir(), a.pos[0])
		if err != nil {
			return err
		}
		if !isSymlink(link) {
			return fail("~/%s is not an existing symlink (use 'link add' to create one)", a.pos[0])
		}
		return c.doLinkAdd(linkAddOpts{target: a.pos[1], name: a.pos[0], force: true, relative: a.has("relative"),
			allowMissing: a.has("allow-missing")})
	case "remove":
		return c.linkRemove(rest)
	case "discover":
		return c.linkDiscover(rest)
	case "track", "untrack":
		return c.linkTrack(rest, args[0] == "track")
	case "folders":
		return c.linkFolders(rest)
	case "backup":
		a, err := parseArgs(rest, "note=")
		if err != nil {
			return err
		}
		note := a.str("note")
		if note == "" {
			note = "manual"
		}
		home := homeDir()
		s, err := saveLinkSnap(home, managedLinks(home, currentEntriesQuiet(), mountedPoints()), note, false)
		if err != nil {
			return err
		}
		c.pf("Link snapshot %s saved (%d links) to %s\n", s.ID, len(s.Links), s.File)
		return nil
	case "restore":
		a, err := parseArgs(rest, "force")
		if err != nil {
			return err
		}
		ref := "latest"
		if len(a.pos) > 0 {
			ref = a.pos[0]
		}
		snaps := listLinkSnaps()
		if len(snaps) == 0 {
			return fail("no link snapshots in %s", backupDir())
		}
		var snap *LinkSnap
		for i := range snaps {
			if ref == "latest" && i == 0 || ref != "latest" && strings.HasPrefix(snaps[i].ID, ref) {
				snap = &snaps[i]
				break
			}
		}
		if snap == nil {
			return fail("no link snapshot matching '%s'", ref)
		}
		home := homeDir()
		c.pf("Restoring link snapshot %s (%s)\n\n", snap.ID, snap.Note)
		plan, err := planLinks(home, snap.Links, snap.Home, a.has("force"))
		if err != nil {
			return err
		}
		return c.runLinkPlan(home, plan, "restore of "+snap.ID, nil)
	case "import":
		a, err := parseArgs(rest, "force", "skip-missing")
		if err != nil {
			return err
		}
		if len(a.pos) != 1 {
			return fail("link import needs a bundle file")
		}
		bd, err := readBundle(a.pos[0])
		if err != nil {
			return err
		}
		home := homeDir()
		plan, err := planLinks(home, bd.Links, bd.Home, a.has("force"))
		if err != nil {
			return err
		}
		if a.has("skip-missing") {
			for i, p := range plan {
				if (p.action == "create" || p.action == "replace") && strings.Contains(p.note, "missing") {
					plan[i] = planRow{"skip", p.name, p.target, "target missing"}
				}
			}
		}
		return c.runLinkPlan(home, plan, "link import of "+filepath.Base(a.pos[0]), &Settings{KeyFolders: bd.KeyFolders, TrackedLinks: bd.TrackedLinks})
	}
	return fail("unknown link action '%s'", args[0])
}

type linkAddOpts struct {
	target, name                                                    string
	adopt, force, relative, mkdirTarget, allowMissing, replaceEmpty bool
}

func (c *Ctx) doLinkAdd(o linkAddOpts) error {
	home := homeDir()
	t := o.target
	if t == "~" || strings.HasPrefix(t, "~/") {
		t = filepath.Join(home, strings.TrimPrefix(t, "~"))
	}
	target, err := filepath.Abs(t)
	if err != nil {
		return err
	}
	name := o.name
	if name == "" {
		name = filepath.Base(strings.TrimRight(target, "/"))
	}
	if name == "" || name == "/" || name == "." {
		return fail("could not derive a link name; pass NAME")
	}
	link, err := linkPath(home, name)
	if err != nil {
		return err
	}
	if link == target || strings.HasPrefix(target, link+"/") {
		return fail("~/%s can't link to itself or to a folder inside it (%s)", name, target)
	}
	ents := currentEntriesQuiet()
	mnt := fstabMountFor(target, ents)
	replacing := o.replaceEmpty && isEmptyDir(link) && !mountedPoints()[link]
	adopting := o.adopt && lexists(link) && !isSymlink(link) && !replacing
	if (adopting || o.mkdirTarget) && mnt != nil && !mountedPoints()[mnt.File] {
		return fail("%s is under %s, which is in fstab but not mounted. Writing now would put data on the wrong disk (underneath the mount point).\n       Mount it first: sudo mount %s", target, mnt.File, mnt.File)
	}
	var plan []string
	if isSymlink(link) {
		cur, _ := os.Readlink(link)
		if linkAbsTarget(link, cur) == target && !o.relative {
			c.pf("~/%s already points to %s\n", name, target)
			return nil
		}
		if !o.force {
			return fail("~/%s is already a symlink to %s; use --force (or 'link set') to repoint it", name, cur)
		}
		plan = append(plan, fmt.Sprintf("replace existing symlink (was -> %s)", cur))
	} else if replacing {
		plan = append(plan, fmt.Sprintf("replace the empty folder ~/%s with a link", name))
	} else if lexists(link) {
		kind := "file"
		if isDir(link) {
			kind = "directory"
		}
		if !o.adopt {
			if o.replaceEmpty && kind == "directory" {
				return fail("~/%s isn't empty, so it can't be replaced by a link. Move its contents first, or use --adopt to move the whole folder to %s.", name, target)
			}
			return fail("%s already exists as a real %s. Use --adopt to move it to %s and link back.", link, kind, target)
		}
		if mountedPoints()[link] {
			return fail("%s is a mount point; refusing to move it", link)
		}
		if lexists(target) {
			return fail("%s already exists; merge the two by hand, then re-run", target)
		}
		if !isDir(filepath.Dir(target)) {
			return fail("%s does not exist; is the disk mounted?", filepath.Dir(target))
		}
		plan = append(plan, fmt.Sprintf("MOVE %s -> %s  (close any apps using it first)", link, target))
	}
	if !adopting && !pathExists(target) {
		switch {
		case o.mkdirTarget:
			plan = append(plan, "create directory "+target)
		case o.allowMissing:
			c.pf("warning: %s does not exist yet; the link will be broken until it does\n", target)
		default:
			return fail("%s does not exist. Use --mkdir-target to create it, or --allow-missing.", target)
		}
	}
	src := target
	if o.relative {
		src, _ = filepath.Rel(filepath.Dir(link), target)
	}
	plan = append(plan, fmt.Sprintf("link ~/%s -> %s", name, src))
	c.pl("Plan:")
	for _, p := range plan {
		c.pl("  - " + p)
	}
	if !c.apply {
		c.pl("\n(dry run: nothing changed)")
		return nil
	}
	c.snapBefore(home, []string{name}, "link add "+name)
	if o.mkdirTarget && !pathExists(target) {
		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}
		chownToUser(target, false)
	}
	if adopting {
		if err := moveTree(link, target); err != nil {
			return fail("move failed: %v", err)
		}
	}
	if replacing {
		// Rmdir only works on an empty folder, so nothing can be lost even if
		// something was saved into it since the preview.
		if err := syscall.Rmdir(link); err != nil {
			return fail("~/%s is no longer empty, so it was left alone: %v", name, err)
		}
	}
	if err := makeSymlink(src, link); err != nil {
		return err
	}
	c.pf("Done: ~/%s -> %s\n", name, src)
	return nil
}

func (c *Ctx) linkRemove(args []string) error {
	a, err := parseArgs(args, "restore-folder")
	if err != nil {
		return err
	}
	if len(a.pos) == 0 {
		return fail("link remove needs at least one NAME")
	}
	home := homeDir()
	type item struct{ name, link, raw string }
	var items []item
	for _, n := range a.pos {
		link, err := linkPath(home, n)
		if err != nil {
			return err
		}
		if !isSymlink(link) {
			return fail("~/%s is not a symlink; only symlinks are ever removed, never real files", n)
		}
		raw, _ := os.Readlink(link)
		items = append(items, item{n, link, raw})
	}
	for _, it := range items {
		c.pf("  remove ~/%s (-> %s)\n", it.name, it.raw)
	}
	if a.has("restore-folder") {
		c.pl("  then create an empty local folder in each link's place")
	}
	c.pl("The data the links point to is not touched.")
	if !c.apply {
		c.pl("\n(dry run: nothing changed)")
		return nil
	}
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.name
	}
	c.snapBefore(home, names, "link remove")
	for _, it := range items {
		if err := os.Remove(it.link); err != nil {
			return err
		}
		if a.has("restore-folder") {
			if err := os.Mkdir(it.link, 0o755); err == nil {
				chownToUser(it.link, false)
			}
		}
	}
	c.pf("Removed %d link(s). Undo from the Backups tab (Home link snapshots).\n", len(items))
	return nil
}

type planRow struct{ action, name, target, note string }

func planLinks(home string, items []LinkRef, oldHome string, force bool) ([]planRow, error) {
	var plan []planRow
	for _, it := range items {
		link, err := linkPath(home, it.Name)
		if err != nil {
			return nil, err
		}
		raw := it.Target
		if oldHome != "" && oldHome != home && (raw == oldHome || strings.HasPrefix(raw, strings.TrimRight(oldHome, "/")+"/")) {
			raw = home + raw[len(oldHome):]
		}
		act := "create"
		if isSymlink(link) {
			cur, _ := os.Readlink(link)
			if cur == raw {
				plan = append(plan, planRow{"skip", it.Name, raw, "already correct"})
				continue
			}
			if !force {
				plan = append(plan, planRow{"skip", it.Name, raw, fmt.Sprintf("currently -> %s; use --force to replace", cur)})
				continue
			}
			act = "replace"
		} else if isEmptyDir(link) {
			act = "replace-empty" // e.g. a fresh install's empty ~/Documents
		} else if lexists(link) {
			plan = append(plan, planRow{"skip", it.Name, raw, "a real file/directory is in the way"})
			continue
		}
		note := ""
		if !pathExists(linkAbsTarget(link, raw)) {
			note = "target missing right now (mount disks first?)"
		}
		plan = append(plan, planRow{act, it.Name, raw, note})
	}
	return plan, nil
}

func (c *Ctx) runLinkPlan(home string, plan []planRow, note string, settings *Settings) error {
	if len(plan) == 0 {
		c.pl("No links in source.")
	} else {
		rows := make([][]string, len(plan))
		for i, p := range plan {
			rows[i] = []string{p.action, p.name, p.target, p.note}
		}
		c.pl(table([]string{"ACTION", "LINK", "TARGET", "NOTE"}, rows, false))
	}
	var todo []planRow
	for _, p := range plan {
		if p.action == "create" || p.action == "replace" || p.action == "replace-empty" {
			todo = append(todo, p)
		}
	}
	extra := settings != nil && (len(settings.KeyFolders) > 0 || len(settings.TrackedLinks) > 0)
	if extra {
		c.pf("\nAlso restores your Key folders list (%d) and tracked links (%d).\n", len(settings.KeyFolders), len(settings.TrackedLinks))
	}
	if len(todo) == 0 && !extra {
		c.pl("\nNothing to do.")
		return nil
	}
	if !c.apply {
		c.pl("\n(dry run: nothing changed)")
		return nil
	}
	names := make([]string, len(todo))
	for i, p := range todo {
		names[i] = p.name
	}
	c.snapBefore(home, names, note)
	sort.Strings(names)
	for _, p := range todo {
		link, _ := linkPath(home, p.name)
		if p.action == "replace-empty" {
			if err := syscall.Rmdir(link); err != nil {
				c.pf("skipped ~/%s: it is no longer empty\n", p.name)
				continue
			}
		}
		if err := makeSymlink(p.target, link); err != nil {
			return err
		}
	}
	if extra {
		s := loadSettings()
		s.KeyFolders = append(s.KeyFolders, settings.KeyFolders...)
		s.TrackedLinks = append(s.TrackedLinks, settings.TrackedLinks...)
		if err := saveSettings(s); err != nil {
			return err
		}
	}
	c.pf("Done: %d link(s) written. Check them on the Home links tab.\n", len(todo))
	return nil
}
