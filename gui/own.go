package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Filesystems that store Linux owners, so `chown` works on them.
var ownableFS = map[string]bool{"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true,
	"f2fs": true, "jfs": true, "reiserfs": true, "zfs": true, "tmpfs": true, "bcachefs": true, "nilfs2": true}

// Windows-family filesystems: no Linux owners; ownership comes from the uid=/gid= mount options.
var windowsFS = map[string]bool{"vfat": true, "fat": true, "msdos": true, "exfat": true,
	"ntfs": true, "ntfs3": true, "ntfs-3g": true}

// Folders whose owner must never be changed wholesale, even if a drive is mounted there.
var ownProtected = map[string]bool{"/": true, "/boot": true, "/boot/efi": true, "/efi": true, "/home": true,
	"/usr": true, "/var": true, "/etc": true, "/opt": true, "/root": true, "/tmp": true, "/srv": true,
	"/nix": true, "/snap": true}

// owner returns the uid and gid that own path (without following a link).
func owner(path string) (uid, gid int, ok bool) {
	var st syscall.Stat_t
	if syscall.Lstat(path, &st) != nil {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}

func userIDs() (name string, uid, gid int, err error) {
	u := realUser()
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil {
		return "", 0, 0, fmt.Errorf("can't read your user id")
	}
	return u.Username, uid, gid, nil
}

// canWrite reports whether uid/gid may create files in dir, from its mode bits.
func canWrite(dir string, uid, gid int) bool {
	var st syscall.Stat_t
	if syscall.Stat(dir, &st) != nil {
		return false
	}
	m := st.Mode
	switch {
	case uid == 0:
		return true
	case int(st.Uid) == uid:
		return m&0o200 != 0
	case int(st.Gid) == gid:
		return m&0o020 != 0
	}
	return m&0o002 != 0
}

// ownPlan walks what `own` would change. Not recursive: the top folder only.
// It stays on the drive (other drives mounted inside are skipped and listed)
// and never follows links.
type ownPlan struct {
	total, foreign int
	skipped        []string
	capped         bool
}

const ownWalkCap = 2_000_000

func planOwn(root string, recursive bool, uid, gid int) ownPlan {
	var p ownPlan
	count := func(path string) {
		p.total++
		if u, g, ok := owner(path); ok && (u != uid || g != gid) {
			p.foreign++
		}
	}
	count(root)
	if !recursive {
		return p
	}
	var rootSt syscall.Stat_t
	syscall.Lstat(root, &rootSt)
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == root {
			return nil
		}
		if p.total >= ownWalkCap {
			p.capped = true
			return filepath.SkipAll
		}
		if d.IsDir() {
			var st syscall.Stat_t
			if syscall.Lstat(path, &st) == nil && st.Dev != rootSt.Dev {
				p.skipped = append(p.skipped, path)
				return filepath.SkipDir
			}
		}
		count(path)
		return nil
	})
	return p
}

// cmdOwn makes you the owner of a mounted drive's folder (`--recursive`: and
// everything on the drive below it), the equivalent of
// `sudo chown [-R] you:you /mnt/data`, with safety checks.
func (c *Ctx) cmdOwn(args []string) error {
	a, err := parseArgs(args, "recursive", "expect=")
	if err != nil {
		return err
	}
	if len(a.pos) != 1 {
		return fail("own needs one target: a mount point or entry number")
	}
	ft, err := loadFstab()
	if err != nil {
		return err
	}
	ie, err := findExpected(ft, a)
	if err != nil {
		return err
	}
	e := ie.E
	dir := e.File
	switch {
	case !e.Enabled:
		return fail("%s is disabled; enable and mount it first", dir)
	case e.Fstype == "swap" || dir == "none" || dir == "swap":
		return fail("%s is swap, not a folder", e.Spec)
	case ownProtected[filepath.Clean(dir)] || systemMounts[dir]:
		return fail("%s is a system folder; changing its owner could break your system, so fstab Manager won't do it", dir)
	case !mountedPoints()[dir]:
		return fail("%s isn't mounted. Mount it first (Mount now); otherwise the empty folder underneath would change, not the drive", dir)
	case windowsFS[e.Fstype]:
		name, uid, gid, _ := userIDs()
		return fail("%s is %s, which can't store Linux owners. Make %s the owner with the mount options instead: "+
			"set %s --add-options uid=%d,gid=%d, then unmount and mount it again", dir, e.Fstype, name, dir, uid, gid)
	case e.Fstype != "auto" && !ownableFS[e.Fstype]:
		return fail("%s (%s) isn't a filesystem fstab Manager changes owners on", dir, e.Fstype)
	}
	name, uid, gid, err := userIDs()
	if err != nil {
		return err
	}
	if uid == 0 {
		return fail("can't tell which user should own it (running as root without sudo or the password prompt)")
	}
	recursive := a.has("recursive")
	p := planOwn(dir, recursive, uid, gid)
	scope := "the top folder only"
	if recursive {
		scope = "everything on the drive under it"
	}
	c.pf("Make %s (uid %d) the owner of %s: %s.\n", name, uid, dir, scope)
	if recursive {
		more := ""
		if p.capped {
			more = " (counted the first " + strconv.Itoa(ownWalkCap) + ")"
		}
		c.pf("  %d item(s)%s, %d owned by someone else now\n", p.total, more, p.foreign)
		for _, s := range p.skipped {
			c.pf("  skipping %s: another drive is mounted there\n", s)
		}
	} else if u, _, ok := owner(dir); ok && u != uid {
		c.pf("  now owned by uid %d\n", u)
	}
	if p.foreign == 0 {
		c.pl("Nothing to do: it already belongs to " + name + ".")
		return nil
	}
	c.pl("Permissions (who may read or write) are not changed, only the owner.")
	if !c.apply {
		c.pl("\n(dry run: nothing changed)")
		return nil
	}
	if os.Geteuid() != 0 {
		return fail("changing the owner needs administrator rights; re-run with sudo")
	}
	changed, failed := 0, 0
	var firstErr error
	do := func(path string) {
		if u, g, ok := owner(path); ok && u == uid && g == gid {
			return
		}
		if err := os.Lchown(path, uid, gid); err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
			return
		}
		changed++
	}
	do(dir)
	if recursive {
		var rootSt syscall.Stat_t
		syscall.Lstat(dir, &rootSt)
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || path == dir {
				return nil
			}
			if d.IsDir() {
				var st syscall.Stat_t
				if syscall.Lstat(path, &st) == nil && st.Dev != rootSt.Dev {
					return filepath.SkipDir
				}
			}
			do(path)
			return nil
		})
	}
	c.pf("\n✓ %s now owns %d item(s) in %s\n", name, changed, dir)
	if failed > 0 {
		return &exitErr{1, fmt.Sprintf("%d item(s) could not be changed (first error: %v)", failed, firstErr)}
	}
	return nil
}

// ownershipFinding explains a mounted drive you can't write to, and offers the fix.
func ownershipFinding(n int, e *Entry, mounted map[string]bool) *Finding {
	dir := e.File
	if !e.Enabled || !mounted[dir] || systemMounts[dir] || ownProtected[filepath.Clean(dir)] || !strings.HasPrefix(dir, "/") {
		return nil
	}
	name, uid, gid, err := userIDs()
	if err != nil || uid == 0 || canWrite(dir, uid, gid) {
		return nil
	}
	ou, _, _ := owner(dir)
	who := "uid " + strconv.Itoa(ou)
	if ou == 0 {
		who = "root"
	}
	switch {
	case windowsFS[e.Fstype]:
		if e.HasOpt("uid=" + strconv.Itoa(uid)) {
			return nil
		}
		return &Finding{Level: "warn",
			Text: fmt.Sprintf("You can't save files on %s: it belongs to %s", dir, who),
			Why: fmt.Sprintf("%s drives can't store Linux owners, so the owner comes from the mount options. "+
				"Without uid=/gid= everything on it belongs to root.", e.Fstype),
			Fix: fmt.Sprintf("Add uid=%d,gid=%d so the files belong to %s. It takes effect the next time the drive "+
				"is mounted (unplug and replug it, or restart).", uid, gid, name),
			Actions: []FindingAction{{"Make me the owner", guarded("set", n, e.Spec,
				"--add-options", fmt.Sprintf("uid=%d,gid=%d", uid, gid))}}}
	case ownableFS[e.Fstype] || e.Fstype == "auto":
		return &Finding{Level: "warn",
			Text: fmt.Sprintf("You can't save files on %s: it belongs to %s", dir, who),
			Why: "A newly formatted drive belongs to root, so only an administrator can create files on it. " +
				"Apps fail with “permission denied” when they try to save there.",
			Fix: fmt.Sprintf("Make %s the owner. “Top folder only” lets you create files and folders there; "+
				"“everything inside” also changes files already on the drive.", name),
			Actions: []FindingAction{{"Take ownership", guarded("own", n, e.Spec)}}}
	}
	return nil
}
