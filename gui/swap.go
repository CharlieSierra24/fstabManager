package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// ---- what swap there is ---------------------------------------------------

// Paths read for swap state; tests point them elsewhere.
var (
	procSwaps   = "/proc/swaps"
	procMeminfo = "/proc/meminfo"
)

// SwapItem is one swap area: from fstab, from /proc/swaps (active now), or both.
type SwapItem struct {
	Name      string `json:"name"`      // file path or device path
	Kind      string `json:"kind"`      // file, partition, zram
	Size      int64  `json:"size"`      // bytes
	Used      int64  `json:"used"`      // bytes, when active
	Priority  int    `json:"priority"`  // when active
	Active    bool   `json:"active"`    // in use now
	Entry     int    `json:"entry"`     // fstab entry number, 0 = not in fstab
	Spec      string `json:"spec"`      // fstab device field
	Enabled   bool   `json:"enabled"`   // fstab entry not disabled
	Missing   bool   `json:"missing"`   // fstab entry whose file or device isn't there
	Hibernate bool   `json:"hibernate"` // the system resumes from hibernation here
	FS        string `json:"fs"`        // swap files: filesystem holding it
	Free      int64  `json:"free"`      // swap files: free space on that filesystem
}

type SwapState struct {
	MemTotal     int64      `json:"mem_total"`
	MemAvailable int64      `json:"mem_available"`
	Items        []SwapItem `json:"items"`
	Suggest      string     `json:"suggest"` // path for a new swap file
	RootFree     int64      `json:"root_free"`
	RootFS       string     `json:"root_fs"`
}

type activeSwap struct {
	name       string
	kind       string
	size, used int64
	prio       int
}

func readProcSwaps() []activeSwap {
	var out []activeSwap
	b, err := os.ReadFile(procSwaps)
	if err != nil {
		return out
	}
	lines := strings.Split(string(b), "\n")
	for _, l := range lines[min(1, len(lines)):] {
		f := strings.Fields(l)
		if len(f) < 5 {
			continue
		}
		size, _ := strconv.ParseInt(f[2], 10, 64)
		used, _ := strconv.ParseInt(f[3], 10, 64)
		prio, _ := strconv.Atoi(f[4])
		name := unescape(strings.TrimSuffix(f[0], " (deleted)"))
		out = append(out, activeSwap{name, f[1], size * 1024, used * 1024, prio})
	}
	return out
}

func meminfo() (total, avail int64) {
	f, err := os.Open(procMeminfo)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Fields(sc.Text())
		if len(p) < 2 {
			continue
		}
		n, _ := strconv.ParseInt(p[1], 10, 64)
		switch p[0] {
		case "MemTotal:":
			total = n * 1024
		case "MemAvailable:":
			avail = n * 1024
		}
	}
	return
}

func isSwapFileSpec(spec string) bool {
	return strings.HasPrefix(spec, "/") && !strings.HasPrefix(spec, "/dev/")
}

func isZram(name string) bool { return strings.HasPrefix(filepath.Base(name), "zram") }

func swapState(ents []IdxEntry) SwapState {
	st := SwapState{Items: []SwapItem{}}
	st.MemTotal, st.MemAvailable = meminfo()
	active := readProcSwaps()
	seen := map[string]bool{}
	byPath := map[string]activeSwap{}
	for _, a := range active {
		byPath[realpath(a.name)] = a
	}
	for i, ie := range ents {
		e := ie.E
		if e.Fstype != "swap" {
			continue
		}
		it := SwapItem{Name: e.Spec, Kind: "partition", Entry: i + 1, Spec: e.Spec, Enabled: e.Enabled}
		if isSwapFileSpec(e.Spec) {
			it.Kind = "file"
		}
		r := resolve(e.Spec)
		if r == "" {
			it.Missing = true
		} else {
			if it.Kind == "partition" {
				it.Name = r
			}
			if a, ok := byPath[realpath(r)]; ok {
				it.Active, it.Size, it.Used, it.Priority = true, max(a.size, areaSize(r, it.Kind)), a.used, a.prio
				seen[realpath(r)] = true
			} else {
				it.Size = areaSize(r, it.Kind)
			}
		}
		if isZram(it.Name) {
			it.Kind = "zram"
		}
		fillSwapItem(&it)
		st.Items = append(st.Items, it)
	}
	for _, a := range active {
		if seen[realpath(a.name)] {
			continue
		}
		it := SwapItem{Name: a.name, Kind: a.kind, Size: a.size, Used: a.used, Priority: a.prio, Active: true}
		if a.kind == "file" {
			it.Size = max(a.size, areaSize(a.name, "file")) // /proc/swaps leaves out the header page
		}
		if isZram(a.name) {
			it.Kind = "zram"
		} else if a.kind != "file" {
			it.Kind = "partition"
		}
		fillSwapItem(&it)
		st.Items = append(st.Items, it)
	}
	st.Suggest = suggestSwapPath(st.Items)
	if ft, _ := fsAt("/"); ft != "" {
		st.RootFS = ft
		st.RootFree = freeSpace("/")
	}
	return st
}

func fillSwapItem(it *SwapItem) {
	if it.Missing {
		return
	}
	it.Hibernate = hibernatesTo(it.Name, it.Kind == "file")
	if it.Kind == "file" {
		it.FS, _ = fsAt(filepath.Dir(it.Name))
		it.Free = freeSpace(filepath.Dir(it.Name))
	}
}

func areaSize(path, kind string) int64 {
	if kind == "file" {
		if fi, err := os.Stat(path); err == nil {
			return fi.Size()
		}
		return 0
	}
	b, err := os.ReadFile(filepath.Join("/sys/class/block", filepath.Base(path), "size"))
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n * 512
}

// suggestSwapPath: a free name for a new swap file at the top of the system drive.
func suggestSwapPath(items []SwapItem) string {
	used := map[string]bool{}
	for _, it := range items {
		used[it.Name] = true
	}
	for _, p := range []string{"/swapfile", "/swap.img", "/swapfile2", "/swapfile3"} {
		if !used[p] && !lexists(p) {
			return p
		}
	}
	return "/swapfile-new"
}

// fsAt returns the filesystem type and mount point holding path.
func fsAt(path string) (string, string) {
	b, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		return "", ""
	}
	p := realpath(path)
	best, bestType := "", ""
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		mp := unescape(f[1])
		if (p == mp || mp == "/" || strings.HasPrefix(p, mp+"/")) && len(mp) >= len(best) {
			best, bestType = mp, f[2]
		}
	}
	return bestType, best
}

func freeSpace(dir string) int64 {
	var s syscall.Statfs_t
	if syscall.Statfs(dir, &s) != nil {
		return 0
	}
	return int64(s.Bavail) * int64(s.Bsize)
}

// hibernatesTo reports whether the kernel is set to resume from this swap area.
func hibernatesTo(path string, file bool) bool {
	b, err := os.ReadFile("/sys/power/resume")
	dev := strings.TrimSpace(string(b))
	if err != nil || dev == "" || dev == "0:0" {
		return false
	}
	var st syscall.Stat_t
	if syscall.Stat(path, &st) != nil {
		return false
	}
	id := st.Rdev
	if file {
		ob, _ := os.ReadFile("/sys/power/resume_offset")
		if off := strings.TrimSpace(string(ob)); off == "" || off == "0" {
			return false
		}
		id = st.Dev
	}
	return fmt.Sprintf("%d:%d", (id>>8)&0xfff|(id>>32)&^0xfff, id&0xff|(id>>12)&^0xff) == dev
}

// ---- sizes ------------------------------------------------------------------

var sizeRe = regexp.MustCompile(`(?i)^\s*([0-9]+(?:\.[0-9]+)?)\s*([KMGT])(?:I?B)?\s*$`)

const (
	MiB          = int64(1) << 20
	GiB          = int64(1) << 30
	minSwapSize  = 128 * MiB
	keepFree     = 2 * GiB   // never fill a drive to the brim with swap
	swapoffSlack = 256 * MiB // memory left over after moving swap back into RAM
)

func parseSwapSize(s string) (int64, error) {
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fail("give a size like 4G or 512M (not %q)", s)
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	mult := map[string]float64{"K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}[strings.ToUpper(m[2])]
	n := int64(v*mult) / MiB * MiB // whole MiB
	if n < minSwapSize {
		return 0, fail("a swap file must be at least %s", human(minSwapSize))
	}
	return n, nil
}

func human(n int64) string {
	switch {
	case n >= GiB:
		v := float64(n) / float64(GiB)
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d GiB", int64(v))
		}
		return fmt.Sprintf("%.1f GiB", v)
	case n >= MiB:
		return fmt.Sprintf("%d MiB", n/MiB)
	}
	return fmt.Sprintf("%d KiB", n/1024)
}

// ---- swap signature -----------------------------------------------------------

// swapHeader reads a swap area's signature; ok is false if it isn't one (or can't be read).
// The UUID is kept when a file is recreated, so anything that names it still works.
func swapHeader(path string) (uuid string, ok bool) {
	uuid, ok, _ = swapSig(path)
	return
}

// swapSig is swapHeader that also says whether the file could be read at all. Swap files are
// readable only by root, and previews run as the user, so "can't read" must not count as "not swap".
func swapSig(path string) (uuid string, ok bool, readable bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, false
	}
	readable = true
	defer f.Close()
	buf := make([]byte, 65536)
	n, _ := f.ReadAt(buf, 0)
	buf = buf[:n]
	for _, ps := range []int{4096, 8192, 16384, 65536} {
		if ps <= len(buf) && bytes.Equal(buf[ps-10:ps], []byte("SWAPSPACE2")) {
			ok = true
			break
		}
	}
	if !ok || len(buf) < 1052 {
		return "", ok, true
	}
	u := buf[1036:1052]
	if bytes.Equal(u, make([]byte, 16)) {
		return "", true, true
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16]), true, true
}

const checkLater = "(Only root can read it, so it's checked to be a swap file when you apply.)"

// ---- the swap command ------------------------------------------------------------

var swapFileFS = map[string]bool{"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true}

// Folders a swap file must not go in.
var swapForbidden = []string{"/proc", "/sys", "/dev", "/run", "/tmp", "/boot", "/etc", "/usr", "/bin", "/sbin",
	"/lib", "/lib32", "/lib64", "/snap", "/var/tmp"}

func (c *Ctx) cmdSwap(args []string) error {
	if len(args) == 0 {
		return fail("swap needs an action: list, create, resize, remove, on, off or setup")
	}
	rest := args[1:]
	switch args[0] {
	case "list":
		return c.swapList()
	case "create":
		return c.swapCreate(rest)
	case "resize":
		return c.swapResize(rest)
	case "remove":
		return c.swapRemove(rest)
	case "on":
		return c.swapOnOff(rest, true)
	case "off":
		return c.swapOnOff(rest, false)
	case "setup":
		return c.swapSetup(rest)
	}
	return fail("unknown swap action '%s'", args[0])
}

func (c *Ctx) swapList() error {
	fs, err := loadFstab()
	if err != nil {
		return err
	}
	st := swapState(fs.Entries())
	c.pf("Memory: %s, %s available now\n\n", human(st.MemTotal), human(st.MemAvailable))
	if len(st.Items) == 0 {
		c.pl("No swap is set up.")
		return nil
	}
	var rows [][]string
	for _, it := range st.Items {
		state := "off"
		switch {
		case it.Active:
			state = "on"
		case !it.Enabled && it.Entry > 0:
			state = "disabled"
		case it.Missing:
			state = "missing"
		}
		n, fsCol := "–", "not in fstab"
		if it.Entry > 0 {
			n, fsCol = strconv.Itoa(it.Entry), "in fstab"
		}
		used := "–"
		if it.Active {
			used = human(it.Used)
		}
		size := "–"
		if it.Size > 0 {
			size = human(it.Size)
		}
		rows = append(rows, []string{n, it.Name, it.Kind, size, used, state, fsCol})
	}
	c.pl(table([]string{"#", "Swap", "Kind", "Size", "Used", "State", "fstab"}, rows, false))
	return nil
}

// swapTarget finds a swap area by fstab entry number, path or device.
func swapTarget(fs *Fstab, target, expect string) (SwapItem, *IdxEntry, error) {
	ents := fs.Entries()
	st := swapState(ents)
	var hits []SwapItem
	for _, it := range st.Items {
		if strconv.Itoa(it.Entry) == target && it.Entry > 0 || it.Name == target || it.Spec == target ||
			(it.Name != "" && realpath(target) == realpath(it.Name) && !it.Missing) {
			hits = append(hits, it)
		}
	}
	if n, err := strconv.Atoi(target); err == nil && len(hits) == 0 && n >= 1 && n <= len(ents) {
		return SwapItem{}, nil, fail("entry %d isn't swap", n)
	}
	if len(hits) == 0 {
		return SwapItem{}, nil, fail("no swap matches '%s' (use a swap file path, device, or # from 'swap list')", target)
	}
	if len(hits) > 1 {
		return SwapItem{}, nil, fail("'%s' matches several swap areas; use the # from 'swap list'", target)
	}
	it := hits[0]
	if expect != "" && it.Spec != expect && it.Name != expect {
		return it, nil, fail("swap has changed since this was shown (%s is now %s); refresh and try again", target, it.Name)
	}
	if it.Entry == 0 {
		return it, nil, nil
	}
	ie := ents[it.Entry-1]
	return it, &ie, nil
}

// canSwapOff checks there is room in memory for what's swapped out now.
func canSwapOff(it SwapItem) error {
	if !it.Active || it.Used == 0 {
		return nil
	}
	_, avail := meminfo()
	if it.Used+swapoffSlack > avail {
		return fail("%s holds %s right now and only %s of memory is free, so it can't be turned off safely. "+
			"Close some programs (or add another swap area first) and try again", it.Name, human(it.Used), human(avail))
	}
	return nil
}

func swapLine(spec string, prio string) *Entry {
	opts := "sw"
	if prio != "" {
		opts = "sw,pri=" + prio
	}
	return &Entry{Spec: spec, File: "none", Fstype: "swap", Options: opts, Enabled: true}
}

func needRoot(what string) error {
	if os.Geteuid() != 0 {
		return fail("%s needs administrator rights; re-run with sudo", what)
	}
	return nil
}

func swapOnCmd(path string, e *Entry) error {
	args := []string{}
	if e != nil {
		for _, o := range e.Opts() {
			if strings.HasPrefix(o, "pri=") {
				args = append(args, "-p", strings.TrimPrefix(o, "pri="))
			}
			if o == "discard" || strings.HasPrefix(o, "discard=") {
				args = append(args, "--"+o)
			}
		}
	}
	o, er, code := runCmd("swapon", append(args, path)...)
	if code != 0 {
		return fmt.Errorf("swapon: %s", strings.TrimSpace(er+" "+o))
	}
	return nil
}

func swapOffCmd(path string) error {
	o, er, code := runCmd("swapoff", path)
	if code != 0 {
		return fmt.Errorf("swapoff: %s", strings.TrimSpace(er+" "+o))
	}
	return nil
}

// checkSwapPath: where a new swap file may go. Returns its filesystem type.
func checkSwapPath(path string, size int64, reuse int64) (string, error) {
	if !strings.HasPrefix(path, "/") {
		return "", fail("the swap file path must start with /")
	}
	path = filepath.Clean(path)
	for _, p := range swapForbidden {
		if path == p || strings.HasPrefix(path, p+"/") {
			return "", fail("a swap file can't go in %s; try /swapfile", p)
		}
	}
	dir := filepath.Dir(path)
	if !isDir(dir) {
		return "", fail("folder %s doesn't exist", dir)
	}
	ft, mp := fsAt(dir)
	if !swapFileFS[ft] {
		return "", fail("%s is on %s (mounted at %s); Linux can only use swap files on ext4, xfs or btrfs", path, ft, mp)
	}
	free := freeSpace(dir) + reuse
	if size+keepFree > free {
		return "", fail("not enough free space on %s for %s of swap: %s free, and %s must stay free",
			mp, human(size), human(free), human(keepFree))
	}
	return ft, nil
}

// makeSwapFile creates path as a swap area of size bytes (keeping uuid if given).
func makeSwapFile(path string, size int64, ft, uuid string) error {
	cleanup := func(err error) error { os.Remove(path); return err }
	made := false
	if ft == "btrfs" && have("btrfs") {
		if _, _, code := runCmd("btrfs", "filesystem", "mkswapfile", "--size", fmt.Sprintf("%dm", size/MiB), path); code == 0 {
			made = true
		} else {
			os.Remove(path)
		}
	}
	if !made {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		f.Close()
		if ft == "btrfs" {
			// no copy-on-write and no compression: required for a swap file on btrfs
			if _, er, code := runCmd("chattr", "+C", path); code != 0 {
				return cleanup(fmt.Errorf("chattr +C: %s", strings.TrimSpace(er)))
			}
		}
		if _, _, code := runCmd("fallocate", "-l", strconv.FormatInt(size, 10), path); code != 0 {
			if err := zeroFill(path, size); err != nil {
				return cleanup(err)
			}
		}
	}
	os.Chmod(path, 0o600)
	os.Chown(path, 0, 0)
	margs := []string{}
	if uuid != "" {
		margs = append(margs, "-U", uuid)
	}
	if _, er, code := runCmd("mkswap", append(margs, path)...); code != 0 {
		return cleanup(fmt.Errorf("mkswap: %s", strings.TrimSpace(er)))
	}
	return nil
}

func zeroFill(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, MiB)
	for left := size; left > 0; left -= MiB {
		if _, err := f.Write(buf[:min(left, MiB)]); err != nil {
			return err
		}
	}
	return f.Sync()
}

func (c *Ctx) swapCreate(args []string) error {
	a, err := parseArgs(args, "size=", "priority=")
	if err != nil {
		return err
	}
	if len(a.pos) != 1 || !a.has("size") {
		return fail("usage: swap create PATH --size SIZE   (for example: swap create /swapfile --size 4G)")
	}
	path := filepath.Clean(a.pos[0])
	size, err := parseSwapSize(a.str("size"))
	if err != nil {
		return err
	}
	prio := a.str("priority")
	if prio != "" {
		if n, err := strconv.Atoi(prio); err != nil || n < -1 || n > 32767 {
			return fail("--priority must be a number from -1 to 32767")
		}
	}
	if lexists(path) {
		return fail("%s already exists; choose another name, or resize it if it's a swap file", path)
	}
	ft, err := checkSwapPath(path, size, 0)
	if err != nil {
		return err
	}
	fs, err := loadFstab()
	if err != nil {
		return err
	}
	for _, ie := range fs.Entries() {
		if ie.E.Spec == path {
			return fail("%s is already in fstab (entry for a file that doesn't exist); remove that entry first", path)
		}
	}
	c.pf("Will create a %s swap file at %s (on %s, %s free there now).\n", human(size), path, ft, human(freeSpace(filepath.Dir(path))))
	c.pl("  1. Make the file and set it up as swap (only root can read it)")
	c.pl("  2. Add it to fstab, so it's used after every restart")
	c.pl("  3. Turn it on now")
	if ft == "btrfs" {
		c.pl("Note: on btrfs, a snapshot of the subvolume holding a swap file fails. If you use snapshots (Timeshift, Snapper),")
		c.pl("put the swap file in a subvolume of its own, such as /swap.")
	}
	c.pl("")
	fs.Append(swapLine(path, prio))
	if !c.apply {
		return c.writeFstab(fs.Text(), "swap create "+path, nil)
	}
	if err := needRoot("creating swap"); err != nil {
		return err
	}
	if err := makeSwapFile(path, size, ft, ""); err != nil {
		return fail("couldn't create the swap file: %v", err)
	}
	c.pf("✓ created %s (%s)\n", path, human(size))
	c.quietNext = true
	if err := c.writeFstab(fs.Text(), "swap create "+path, nil); err != nil {
		os.Remove(path)
		c.pf("Removed %s again, because fstab couldn't be updated.\n", path)
		return err
	}
	if err := swapOnCmd(path, swapLine(path, prio)); err != nil {
		return fail("the swap file is made and in fstab, but turning it on failed (%v); it will be used after a restart", err)
	}
	c.pf("✓ %s is on. You now have %s more swap.\n", path, human(size))
	return nil
}

func hibernateNote(c *Ctx, it SwapItem, what string) {
	if it.Hibernate {
		c.pf("Warning: this computer hibernates to %s. After you %s, hibernation won't work until you update the\n", it.Name, what)
		c.pl("resume settings (resume= / resume_offset= in the boot options, then update-initramfs and update-grub).")
	}
}

func (c *Ctx) swapResize(args []string) error {
	a, err := parseArgs(args, "size=", "expect=")
	if err != nil {
		return err
	}
	if len(a.pos) != 1 || !a.has("size") {
		return fail("usage: swap resize TARGET --size SIZE   (TARGET: swap file path or # from 'swap list')")
	}
	size, err := parseSwapSize(a.str("size"))
	if err != nil {
		return err
	}
	fs, err := loadFstab()
	if err != nil {
		return err
	}
	it, ie, err := swapTarget(fs, a.pos[0], a.str("expect"))
	if err != nil {
		return err
	}
	switch {
	case it.Kind == "zram":
		return fail("%s is compressed swap in memory (zram); change its size in /etc/systemd/zram-generator.conf", it.Name)
	case it.Kind != "file":
		return fail("%s is a partition. A partition's size can only be changed by repartitioning the disk "+
			"(KDE Partition Manager or GParted, from a live USB). An easier way to get more swap: add a swap file", it.Name)
	case it.Missing:
		return fail("%s doesn't exist; remove its fstab entry, or create it again", it.Name)
	}
	path := it.Name
	cur := areaSize(path, "file")
	if size == cur {
		return fail("%s is already %s", path, human(size))
	}
	uuid, ok, readable := swapSig(path)
	if readable && !ok || !readable && c.apply {
		return fail("%s isn't set up as swap (it has no swap signature), so fstabctl won't resize it. "+
			"Remove it with Remove (the file is kept), then add a new swap file", path)
	}
	ft, err := checkSwapPath(path, size, cur)
	if err != nil {
		return err
	}
	if err := canSwapOff(it); err != nil {
		return err
	}
	c.pf("Will resize the swap file %s from %s to %s.\n", path, human(cur), human(size))
	if it.Active {
		c.pf("  1. %s\n", offStep(it))
	} else {
		c.pl("  1. (it's off now)")
	}
	c.pl("  2. Make it again at the new size, keeping its UUID")
	turnOn := it.Active || (ie != nil && ie.E.Enabled)
	if turnOn {
		c.pl("  3. Turn it back on")
	}
	c.pl("fstab doesn't change: the entry names the file, not its size.")
	if !readable {
		c.pl(checkLater)
	}
	hibernateNote(c, it, "resize it")
	if !c.apply {
		c.pl("\n(dry run: nothing changed)")
		return nil
	}
	if err := needRoot("resizing swap"); err != nil {
		return err
	}
	if it.Active {
		if err := swapOffCmd(path); err != nil {
			return fail("%v; nothing was changed", err)
		}
		c.pf("✓ turned off %s\n", path)
	}
	if err := os.Remove(path); err != nil {
		return fail("couldn't replace %s: %v", path, err)
	}
	var e *Entry
	if ie != nil {
		e = ie.E
	}
	if err := makeSwapFile(path, size, ft, uuid); err != nil {
		c.pf("✗ couldn't make it at %s: %v\n", human(size), err)
		if err2 := makeSwapFile(path, cur, ft, uuid); err2 == nil {
			if turnOn {
				swapOnCmd(path, e)
			}
			return fail("resize failed; %s was put back at %s", path, human(cur))
		}
		return fail("resize failed and the old file couldn't be put back; create it again with: fstabctl swap create %s --size %s "+
			"(after removing its fstab entry)", path, human(cur))
	}
	c.pf("✓ %s is now %s\n", path, human(size))
	if turnOn {
		if err := swapOnCmd(path, e); err != nil {
			return fail("resized, but turning it on failed (%v); it will be used after a restart", err)
		}
		c.pf("✓ turned on %s\n", path)
	}
	return nil
}

func (c *Ctx) swapRemove(args []string) error {
	a, err := parseArgs(args, "keep-file", "expect=")
	if err != nil {
		return err
	}
	if len(a.pos) != 1 {
		return fail("usage: swap remove TARGET [--keep-file]   (TARGET: path, device or # from 'swap list')")
	}
	fs, err := loadFstab()
	if err != nil {
		return err
	}
	it, ie, err := swapTarget(fs, a.pos[0], a.str("expect"))
	if err != nil {
		return err
	}
	if it.Kind == "zram" {
		return fail("%s is compressed swap in memory (zram), set up by your system. To stop it, remove the "+
			"zram-generator (or systemd-zram-setup) package or edit /etc/systemd/zram-generator.conf", it.Name)
	}
	if err := canSwapOff(it); err != nil {
		return err
	}
	deleteFile := it.Kind == "file" && !a.has("keep-file") && !it.Missing
	keepWhy, unchecked := "", false
	if deleteFile {
		if fi, err := os.Lstat(it.Name); err != nil || !fi.Mode().IsRegular() {
			deleteFile = false
		} else if _, ok, readable := swapSig(it.Name); !readable && !c.apply {
			unchecked = true
		} else if !ok && !it.Active {
			// never delete a file that isn't recognisably swap: keep it and say so
			deleteFile = false
			keepWhy = fmt.Sprintf("%s is kept: it has no swap signature, so it may not be a swap file (or it may hold data). "+
				"If you're sure it isn't needed, delete it yourself: sudo rm %s", it.Name, it.Name)
		}
	}
	c.pf("Will remove swap %s (%s, %s).\n", it.Name, it.Kind, human(it.Size))
	step := 1
	if it.Active {
		c.pf("  %d. %s\n", step, offStep(it))
		step++
	}
	if ie != nil {
		c.pf("  %d. Take it out of fstab\n", step)
		step++
	}
	if deleteFile {
		c.pf("  %d. Delete the file, freeing %s\n", step, human(it.Size))
		if unchecked {
			c.pl("     " + checkLater + " If it isn't one, it's kept.")
		}
	} else if keepWhy != "" {
		c.pl(keepWhy)
	} else if it.Kind == "partition" {
		c.pl("The partition itself is left as it is; reuse it or delete it with a partition editor.")
	}
	others := 0
	for _, x := range swapState(fs.Entries()).Items {
		if x.Active && x.Name != it.Name {
			others++
		}
	}
	if it.Active && others == 0 {
		c.pl("Note: this is your only swap. Without swap, running out of memory closes programs instead of slowing down.")
	}
	hibernateNote(c, it, "remove it")
	c.pl("")
	newText := ""
	if ie != nil {
		fs.Remove(ie.Idx)
		newText = fs.Text()
	}
	if !c.apply {
		if ie != nil {
			return c.writeFstab(newText, "swap remove "+it.Name, nil)
		}
		c.pl("(dry run: nothing changed)")
		return nil
	}
	if err := needRoot("removing swap"); err != nil {
		return err
	}
	if it.Active {
		if err := swapOffCmd(it.Name); err != nil {
			return fail("%v; nothing was changed", err)
		}
		c.pf("✓ turned off %s\n", it.Name)
	}
	if ie != nil {
		c.quietNext = true
		if err := c.writeFstab(newText, "swap remove "+it.Name, nil); err != nil {
			return err
		}
	}
	if deleteFile {
		if err := os.Remove(it.Name); err != nil {
			return fail("couldn't delete %s: %v", it.Name, err)
		}
		c.pf("✓ deleted %s\n", it.Name)
	} else if keepWhy != "" {
		c.pl(keepWhy)
	}
	return nil
}

func (c *Ctx) swapOnOff(args []string, on bool) error {
	a, err := parseArgs(args, "expect=")
	if err != nil {
		return err
	}
	if len(a.pos) != 1 {
		return fail("needs one target: a swap file path, device or # from 'swap list'")
	}
	fs, err := loadFstab()
	if err != nil {
		return err
	}
	it, ie, err := swapTarget(fs, a.pos[0], a.str("expect"))
	if err != nil {
		return err
	}
	if it.Missing {
		return fail("%s isn't there", it.Name)
	}
	if on == it.Active {
		if on {
			return fail("%s is already on", it.Name)
		}
		return fail("%s is already off", it.Name)
	}
	if !on {
		if err := canSwapOff(it); err != nil {
			return err
		}
	} else if it.Kind == "file" {
		if _, ok, readable := swapSig(it.Name); readable && !ok {
			return fail("%s isn't set up as swap (it has no swap signature), so it can't be turned on. "+
				"Remove it (the file is kept), then add a new swap file", it.Name)
		}
	}
	verb := "off"
	if on {
		verb = "on"
	}
	c.pf("Will turn %s %s now. fstab doesn't change", verb, it.Name)
	if ie != nil && !on && ie.E.Enabled {
		c.pl(", so it comes back on after a restart (use Disable to stop that).")
	} else {
		c.pl(".")
	}
	if !c.apply {
		c.pl("\n(dry run: nothing changed)")
		return nil
	}
	if err := needRoot("turning swap " + verb); err != nil {
		return err
	}
	if on {
		var e *Entry
		if ie != nil {
			e = ie.E
		}
		err = swapOnCmd(it.Name, e)
	} else {
		err = swapOffCmd(it.Name)
	}
	if err != nil {
		return fail("%v", err)
	}
	c.pf("✓ %s is %s\n", it.Name, verb)
	return nil
}

// swapSetup makes a partition into swap (if it isn't already), adds it to fstab and turns it on.
func (c *Ctx) swapSetup(args []string) error {
	a, err := parseArgs(args, "erase", "priority=")
	if err != nil {
		return err
	}
	if len(a.pos) != 1 {
		return fail("usage: swap setup DEVICE [--erase]   (for example: swap setup /dev/sdb2)")
	}
	dev := deviceFor(a.pos[0])
	if dev == nil {
		return fail("no partition %s found", a.pos[0])
	}
	if dev.Type != "part" && dev.Type != "lvm" && dev.Type != "crypt" {
		return fail("%s is a %s; choose a partition", dev.Path, dev.Type)
	}
	if len(dev.Mounts) > 0 {
		return fail("%s is in use (mounted at %s)", dev.Path, strings.Join(dev.Mounts, ", "))
	}
	if nonFS[dev.Fstype] {
		return fail("%s holds %s; it is part of another storage setup, so fstabctl won't touch it", dev.Path, dev.Fstype)
	}
	if hasChildren(dev.Path) {
		return fail("%s holds other volumes; fstabctl won't touch it", dev.Path)
	}
	for _, x := range readProcSwaps() {
		if realpath(x.name) == realpath(dev.Path) {
			return fail("%s is already swap and on", dev.Path)
		}
	}
	fs, err := loadFstab()
	if err != nil {
		return err
	}
	for _, ie := range fs.Entries() {
		if resolve(ie.E.Spec) == realpath(dev.Path) && ie.E.Enabled {
			return fail("%s is already in fstab (entry for %s)", dev.Path, ie.E.File)
		}
	}
	format := dev.Fstype != "swap"
	if format && dev.Fstype != "" && !a.has("erase") {
		what := dev.Fstype
		if dev.Label != "" {
			what += ", label " + dev.Label
		}
		return fail("%s holds a %s filesystem. Making it swap erases everything on it; add --erase if that's what you want", dev.Path, what)
	}
	prio := a.str("priority")
	c.pf("Will use %s (%s) as swap.\n", dev.Path, dev.Size)
	step := 1
	if format {
		if dev.Fstype != "" {
			c.pf("  %d. ERASE it: everything on this %s partition", step, dev.Fstype)
			if dev.Label != "" {
				c.pf(" (%s)", dev.Label)
			}
			c.pl(" is lost")
		} else {
			c.pf("  %d. Set it up as swap (it's empty)\n", step)
		}
		step++
	}
	c.pf("  %d. Add it to fstab by UUID\n  %d. Turn it on now\n\n", step, step+1)
	spec := "UUID=" + dev.UUID
	if !c.apply {
		if format {
			spec = "UUID=(made-by-mkswap)"
		}
		fs.Append(swapLine(spec, prio))
		return c.writeFstab(fs.Text(), "swap setup "+dev.Path, nil)
	}
	if err := needRoot("setting up swap"); err != nil {
		return err
	}
	if format {
		if _, er, code := runCmd("mkswap", "-f", dev.Path); code != 0 {
			return fail("mkswap: %s", strings.TrimSpace(er))
		}
		uuid, ok := swapHeader(dev.Path)
		if !ok || uuid == "" {
			return fail("mkswap ran but %s has no swap UUID", dev.Path)
		}
		spec = "UUID=" + uuid
		c.pf("✓ %s is now swap\n", dev.Path)
	}
	fs.Append(swapLine(spec, prio))
	c.quietNext = true
	if err := c.writeFstab(fs.Text(), "swap setup "+dev.Path, nil); err != nil {
		return err
	}
	if err := swapOnCmd(dev.Path, swapLine(spec, prio)); err != nil {
		return fail("added to fstab, but turning it on failed (%v); it will be used after a restart", err)
	}
	c.pf("✓ %s is on\n", dev.Path)
	return nil
}

func deviceFor(s string) *Device {
	r := s
	if !strings.HasPrefix(s, "/") {
		r = resolve(s)
	}
	r = realpath(r)
	for _, d := range lsblk() {
		if d.Path == s || realpath(d.Path) == r {
			d := d
			return &d
		}
	}
	return nil
}

func hasChildren(path string) bool {
	ents, err := os.ReadDir(filepath.Join("/sys/class/block", filepath.Base(realpath(path)), "holders"))
	return err == nil && len(ents) > 0
}

func offStep(it SwapItem) string {
	if it.Used == 0 {
		return "Turn it off (nothing is stored in it right now)"
	}
	return fmt.Sprintf("Turn it off (moves the %s stored in it back into memory)", human(it.Used))
}

// swapFinding: an Overview note when the computer has no swap on at all.
func swapFinding(st SwapState) *Finding {
	for _, it := range st.Items {
		if it.Active {
			return nil
		}
	}
	return &Finding{Level: "info", Text: "This computer has no swap turned on",
		Why: "Swap is disk space the system uses when memory is full. Without it, running out of memory closes programs " +
			"(sometimes the one you're working in) instead of just slowing down.",
		Fix:     "Add a swap file of 4–8 GiB on the Swap tab, or turn on the swap you already have there.",
		Actions: []FindingAction{{"Add a swap file", []string{"swap", "create"}}}}
}
