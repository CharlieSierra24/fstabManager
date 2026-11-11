package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// Ctx carries output and the apply/preview switch for one operation.
type Ctx struct {
	w     io.Writer
	apply bool // false = preview (dry run), true = write
}

func (c *Ctx) pf(f string, a ...any) { fmt.Fprintf(c.w, f, a...) }
func (c *Ctx) pl(a ...any)           { fmt.Fprintln(c.w, a...) }

type exitErr struct {
	code int
	msg  string
}

func (e *exitErr) Error() string { return e.msg }
func fail(f string, a ...any) error {
	return &exitErr{1, fmt.Sprintf(f, a...)}
}

// RunOp executes a CLI-style operation (the same verbs as the Python tool)
// and returns an exit code. All output goes to w.
func RunOp(args []string, apply bool, w io.Writer) int {
	c := &Ctx{w: w, apply: apply}
	if len(args) == 0 {
		fmt.Fprintln(w, "error: no command")
		return 1
	}
	var err error
	rest := args[1:]
	switch args[0] {
	case "add":
		err = c.cmdAdd(rest)
	case "remove":
		err = c.cmdRemove(rest)
	case "enable":
		err = c.cmdToggle(rest, true)
	case "disable":
		err = c.cmdToggle(rest, false)
	case "set":
		err = c.cmdSet(rest)
	case "backup":
		err = c.cmdBackup(rest)
	case "diff":
		err = c.cmdDiff(rest)
	case "restore":
		err = c.cmdRestore(rest)
	case "import":
		err = c.cmdImport(rest)
	case "link":
		err = c.cmdLink(rest)
	case "mount":
		err = c.cmdMount(rest)
	case "repair":
		err = c.cmdRepair(rest)
	default:
		err = fail("unknown command '%s'", args[0])
	}
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exitErr); ok {
		if ee.msg != "" {
			c.pf("error: %s\n", ee.msg)
		}
		return ee.code
	}
	c.pf("error: %v\n", err)
	return 1
}

// ---- argument parsing -------------------------------------------------

type argset struct {
	pos []string
	fl  map[string]string
}

func (a *argset) has(n string) bool   { _, ok := a.fl[n]; return ok }
func (a *argset) str(n string) string { return a.fl[n] }
func (a *argset) intOr(n string, def int) (int, error) {
	v, ok := a.fl[n]
	if !ok {
		return def, nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0, fail("--%s needs a number", n)
	}
	return i, nil
}

// parseArgs: defs look like "fstype:t=" (value flag with short -t) or "force" (switch).
func parseArgs(args []string, defs ...string) (*argset, error) {
	type d struct {
		name string
		val  bool
	}
	keys := map[string]d{}
	for _, def := range defs {
		val := strings.HasSuffix(def, "=")
		def = strings.TrimSuffix(def, "=")
		long, short, _ := strings.Cut(def, ":")
		keys["--"+long] = d{long, val}
		if short != "" {
			keys["-"+short] = d{long, val}
		}
	}
	out := &argset{fl: map[string]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			out.pos = append(out.pos, a)
			continue
		}
		key, val, hasEq := strings.Cut(a, "=")
		def, ok := keys[key]
		if !ok {
			return nil, fail("unknown option %s", key)
		}
		if def.val {
			if !hasEq {
				if i+1 >= len(args) {
					return nil, fail("option %s needs a value", key)
				}
				i++
				val = args[i]
			}
			out.fl[def.name] = val
		} else {
			out.fl[def.name] = "true"
		}
	}
	return out, nil
}

// ---- presets ----------------------------------------------------------

type Preset struct{ Name, Options, Desc string }

var presets = []Preset{
	{"data", "defaults,nofail", "Generic internal disk (ext4/xfs/btrfs). Boot continues if it is missing."},
	{"ssd", "defaults,noatime,nofail", "SSD/NVMe. noatime cuts writes. Prefer the fstrim.timer service over 'discard'."},
	{"usb", "defaults,noauto,nofail,x-systemd.automount,x-systemd.device-timeout=5,x-systemd.idle-timeout=60",
		"Removable/external drive. Mounts on first access, never blocks boot."},
	{"windows", "defaults,nofail,uid={uid},gid={gid},dmask=022,fmask=133",
		"NTFS/exFAT/FAT drive. Files owned by you; no execute bit on files."},
	{"nfs", "defaults,_netdev,nofail,x-systemd.automount,x-systemd.mount-timeout=10",
		"NFS share. Waits for network, mounts on demand, never blocks boot."},
	{"cifs", "defaults,_netdev,nofail,x-systemd.automount,x-systemd.mount-timeout=10,credentials=/etc/samba/credentials,uid={uid},gid={gid}",
		"SMB/CIFS share. Put username/password in the credentials file (chmod 600)."},
}

// presetList marshals to an ordered JSON object {name: [options, description]}.
type presetList []Preset

func (p presetList) MarshalJSON() ([]byte, error) {
	var sb strings.Builder
	sb.WriteByte('{')
	for i, x := range p {
		if i > 0 {
			sb.WriteByte(',')
		}
		k, _ := json.Marshal(x.Name)
		v, _ := json.Marshal([]string{x.Options, x.Desc})
		sb.Write(k)
		sb.WriteByte(':')
		sb.Write(v)
	}
	sb.WriteByte('}')
	return []byte(sb.String()), nil
}

func findPreset(name string) *Preset {
	for i := range presets {
		if presets[i].Name == name {
			return &presets[i]
		}
	}
	return nil
}

func pickPreset(fstype string, d *Device) string {
	switch fstype {
	case "vfat", "exfat", "ntfs", "ntfs3", "ntfs-3g":
		return "windows"
	case "nfs", "nfs4":
		return "nfs"
	case "cifs":
		return "cifs"
	}
	if d != nil && d.Rm {
		return "usb"
	}
	if d != nil && !d.Rota {
		return "ssd"
	}
	return "data"
}

// ---- writing ----------------------------------------------------------

func atomicWrite(path, text string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fstabctl-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(name)
		}
	}()
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if st, err := os.Stat(path); err == nil {
		_ = os.Chmod(name, st.Mode().Perm())
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			_ = os.Chown(name, int(sys.Uid), int(sys.Gid))
		}
	} else {
		_ = os.Chmod(name, 0o644)
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	if have("restorecon") {
		runCmd("restorecon", path)
	}
	return nil
}

func (c *Ctx) showDiff(a, b, fromName, toName string) bool {
	d := unifiedDiff(parseFstab(a).Lines, parseFstab(b).Lines, fromName, toName)
	if len(d) == 0 {
		c.pl("(no changes)")
		return false
	}
	c.pl(strings.Join(d, "\n"))
	return true
}

// writeFstab: validate -> diff -> (preview stops here) -> backup -> atomic write.
func (c *Ctx) writeFstab(newText, note string, post func()) error {
	old, err := readFstab()
	if err != nil {
		return err
	}
	if !strings.HasSuffix(newText, "\n") {
		newText += "\n"
	}
	if old == newText {
		c.pl("Nothing to change.")
		return nil
	}
	pre := map[string]bool{}
	for _, e := range validate(old) {
		pre[e] = true
	}
	var errs []string
	for _, e := range validate(newText) {
		if !pre[e] {
			errs = append(errs, e)
		}
	}
	if len(errs) > 0 {
		c.pl("Refusing to write; problems found:")
		for _, e := range errs {
			c.pf("  - %s\n", e)
		}
		return &exitErr{2, ""}
	}
	c.showDiff(old, newText, "current", "proposed")
	if have("findmnt") {
		if t, err := os.CreateTemp("", "*.fstab"); err == nil {
			t.WriteString(newText)
			t.Close()
			o, e, _ := runCmd("findmnt", "--verify", "--tab-file", t.Name())
			os.Remove(t.Name())
			var notes []string
			for _, l := range strings.Split(o+e, "\n") {
				if strings.Contains(l, "[E]") || strings.Contains(l, "[W]") {
					notes = append(notes, "  "+strings.TrimSpace(l))
				}
			}
			if len(notes) > 0 {
				c.pl("\nfindmnt --verify says (advisory):")
				c.pl(strings.Join(notes, "\n"))
			}
		}
	}
	if !c.apply {
		c.pl("\n(dry run: nothing written)")
		return nil
	}
	target := fstabPath()
	if !pathExists(target) {
		target = filepath.Dir(target)
	}
	if syscall.Access(target, 2) != nil {
		return fail("no permission to write %s; re-run with sudo", fstabPath())
	}
	b, err := makeBackup("auto: before "+note, true)
	if err != nil {
		return fail("could not create backup: %v", err)
	}
	if err := atomicWrite(fstabPath(), newText); err != nil {
		return fail("write failed: %v", err)
	}
	c.pf("\nWritten. Previous version saved as backup %s.\n", b.ID)
	if post != nil {
		post()
	}
	c.pl("Next: 'sudo systemctl daemon-reload' (systemd distros), then 'sudo mount -a' to test.")
	c.pf("Undo any time with: fstabctl restore %s\n", b.ID)
	return nil
}

func loadFstab() (*Fstab, error) {
	t, err := readFstab()
	if err != nil {
		return nil, err
	}
	return parseFstab(t), nil
}

// ---- commands ---------------------------------------------------------

func isNetwork(d string) bool {
	return strings.HasPrefix(d, "//") || (strings.Contains(d, ":") && !strings.HasPrefix(d, "/"))
}

func (c *Ctx) cmdAdd(args []string) error {
	a, err := parseArgs(args, "fstype:t=", "options:o=", "preset:p=", "by=", "dump=", "passno=", "no-mkdir")
	if err != nil {
		return err
	}
	if len(a.pos) == 0 {
		return fail("add needs a device")
	}
	device := a.pos[0]
	devs := lsblk()
	for i := range devs {
		devs[i].Candidate = isCandidate(&devs[i])
	}
	fs, err := loadFstab()
	if err != nil {
		return err
	}
	ents := fs.Entries()
	spec, fstype := device, a.str("fstype")
	var dev *Device
	by := a.str("by")
	if by == "" {
		by = "uuid"
	}
	findDev := func(arg string) *Device {
		real := realpath(arg)
		for i := range devs {
			d := &devs[i]
			if real == realpath(d.Path) || arg == d.Name || (arg != "" && (arg == d.UUID || arg == d.Label)) {
				return d
			}
		}
		return nil
	}
	switch {
	case keyRe.MatchString(device):
		if fstype == "" {
			if r := resolve(device); r != "" {
				if d := findDev(r); d != nil {
					dev, fstype = d, d.Fstype
				}
			}
		}
		if fstype == "" {
			return fail("could not detect the filesystem for %s; pass the type with -t", device)
		}
		if dev == nil {
			if r := resolve(device); r != "" {
				dev = findDev(r)
			}
		}
	case isNetwork(device):
		if fstype == "" {
			return fail("network shares need -t/--fstype (e.g. nfs, nfs4, cifs)")
		}
	default:
		dev = findDev(device)
		if dev == nil {
			return fail("'%s' is not a known block device", device)
		}
		var key, val string
		switch by {
		case "uuid":
			key, val = "UUID", dev.UUID
		case "label":
			key, val = "LABEL", dev.Label
		case "partuuid":
			key, val = "PARTUUID", dev.PartUUID
		case "path":
		default:
			return fail("--by must be uuid, label, partuuid or path")
		}
		switch {
		case key != "" && val != "":
			spec = key + "=" + val
		case key != "":
			fallback := dev.Path
			if dev.UUID != "" {
				fallback = "UUID=" + dev.UUID
			}
			c.pf("note: device has no %s; using %s\n", by, fallback)
			spec = fallback
		default:
			spec = dev.Path
			c.pl("note: /dev/sdX names can change between boots; UUID is safer.")
		}
		if fstype == "" {
			fstype = dev.Fstype
		}
		if fstype == "" {
			return fail("could not detect a filesystem; format it first or pass -t")
		}
	}
	if fstype == "ntfs" {
		if b, _ := os.ReadFile("/proc/filesystems"); strings.Contains(string(b), "ntfs3") {
			fstype = "ntfs3"
		} else {
			fstype = "ntfs-3g"
		}
	}
	mountpoint := "none"
	if fstype != "swap" {
		mountpoint = ""
		if len(a.pos) > 1 {
			mountpoint = a.pos[1]
		}
		if mountpoint == "" {
			name := "share"
			if dev != nil {
				name = dev.Label
				if name == "" {
					name = dev.Name
				}
			}
			mountpoint = "/mnt/" + slug(name)
		}
	}
	if mountpoint != "none" && !strings.HasPrefix(mountpoint, "/") {
		return fail("mount point must be an absolute path")
	}
	pname := a.str("preset")
	if pname == "" {
		pname = pickPreset(fstype, dev)
	}
	pr := findPreset(pname)
	if pr == nil {
		return fail("unknown preset '%s'", pname)
	}
	u := realUser()
	options := a.str("options")
	custom := options != ""
	if !custom {
		options = strings.NewReplacer("{uid}", u.Uid, "{gid}", u.Gid).Replace(pr.Options)
	}
	if fstype == "swap" && !custom {
		options = "sw"
	}
	dump, err := a.intOr("dump", 0)
	if err != nil {
		return err
	}
	pass := 0
	if fstype == "ext2" || fstype == "ext3" || fstype == "ext4" {
		if mountpoint != "/" {
			pass = 2
		}
	}
	if a.has("passno") {
		if pass, err = a.intOr("passno", 0); err != nil {
			return err
		}
	}
	if fstype == "swap" {
		pass = 0
	}
	entry := &Entry{Spec: spec, File: mountpoint, Fstype: fstype, Options: options, Dump: dump, Pass: pass, Enabled: true}
	for _, ie := range ents {
		e := ie.E
		if e.Enabled && mountpoint != "none" && e.File == mountpoint {
			return fail("%s is already in fstab (%s); use 'set' to change it", mountpoint, e.Spec)
		}
		if e.Enabled && e.Spec == spec && fstype != "swap" {
			c.pf("warning: %s already appears in fstab at %s\n", spec, e.File)
		}
	}
	how := "preset: " + pname
	if custom {
		how = "custom options"
	}
	c.pf("\nNew entry (%s):\n  %s\n\n", how, entry.Line())
	fs.Append(entry)
	return c.writeFstab(fs.Text(), "add "+mountpoint, func() {
		if mountpoint != "none" && !a.has("no-mkdir") && !isDir(mountpoint) {
			if os.MkdirAll(mountpoint, 0o755) == nil {
				c.pf("Created mount point %s\n", mountpoint)
			}
		}
	})
}

func (c *Ctx) cmdRemove(args []string) error {
	a, err := parseArgs(args, "force", "expect=")
	if err != nil {
		return err
	}
	if len(a.pos) != 1 {
		return fail("remove needs one target")
	}
	fs, err := loadFstab()
	if err != nil {
		return err
	}
	ie, err := findExpected(fs, a)
	if err != nil {
		return err
	}
	if systemMounts[ie.E.File] && !a.has("force") {
		return fail("%s is a boot-critical mount; use --force if you really mean it", ie.E.File)
	}
	fs.Remove(ie.Idx)
	return c.writeFstab(fs.Text(), "remove "+ie.E.File, nil)
}

func (c *Ctx) cmdToggle(args []string, enable bool) error {
	a, err := parseArgs(args, "force", "expect=")
	if err != nil {
		return err
	}
	if len(a.pos) != 1 {
		return fail("needs one target")
	}
	fs, err := loadFstab()
	if err != nil {
		return err
	}
	ie, err := findExpected(fs, a)
	if err != nil {
		return err
	}
	e := ie.E
	if e.Enabled == enable {
		if enable {
			return fail("%s is already enabled", e.File)
		}
		return fail("%s is already disabled", e.File)
	}
	if !enable && systemMounts[e.File] && !a.has("force") {
		return fail("%s is boot-critical; use --force to disable it", e.File)
	}
	e.Enabled = enable
	fs.Replace(ie.Idx, e)
	verb := "disable"
	if enable {
		verb = "enable"
	}
	return c.writeFstab(fs.Text(), verb+" "+e.File, nil)
}

func (c *Ctx) cmdSet(args []string) error {
	a, err := parseArgs(args, "options:o=", "add-options=", "remove-options=", "fstype:t=", "device=", "mountpoint=", "passno=", "expect=")
	if err != nil {
		return err
	}
	if len(a.pos) != 1 {
		return fail("set needs one target")
	}
	fs, err := loadFstab()
	if err != nil {
		return err
	}
	ie, err := findExpected(fs, a)
	if err != nil {
		return err
	}
	e := ie.E
	if a.has("options") {
		e.Options = a.str("options")
	}
	if a.has("add-options") {
		cur := e.Opts()
		for _, o := range strings.Split(a.str("add-options"), ",") {
			if o != "" && !e.HasOpt(o) {
				cur = append(cur, o)
				e.Options = strings.Join(cur, ",")
			}
		}
	}
	if a.has("remove-options") {
		drop := map[string]bool{}
		for _, o := range strings.Split(a.str("remove-options"), ",") {
			drop[o] = true
		}
		var keep []string
		for _, o := range e.Opts() {
			if !drop[o] {
				keep = append(keep, o)
			}
		}
		e.Options = strings.Join(keep, ",")
		if e.Options == "" {
			e.Options = "defaults"
		}
	}
	if a.has("fstype") {
		e.Fstype = a.str("fstype")
	}
	if a.has("device") {
		e.Spec = a.str("device")
	}
	if a.has("mountpoint") {
		e.File = a.str("mountpoint")
	}
	if a.has("passno") {
		if e.Pass, err = a.intOr("passno", 0); err != nil {
			return err
		}
	}
	fs.Replace(ie.Idx, e)
	return c.writeFstab(fs.Text(), "edit "+e.File, nil)
}

func (c *Ctx) cmdBackup(args []string) error {
	a, err := parseArgs(args, "note=")
	if err != nil {
		return err
	}
	note := a.str("note")
	if note == "" {
		note = "manual"
	}
	b, err := makeBackup(note, false)
	if err != nil {
		return err
	}
	c.pf("Backup %s saved to %s\n", b.ID, b.File)
	return nil
}

func (c *Ctx) cmdDiff(args []string) error {
	a, err := parseArgs(args)
	if err != nil {
		return err
	}
	ref := "latest"
	if len(a.pos) > 0 {
		ref = a.pos[0]
	}
	b, err := findBackup(ref)
	if err != nil {
		return err
	}
	old, err := os.ReadFile(b.File)
	if err != nil {
		return err
	}
	cur, err := readFstab()
	if err != nil {
		return err
	}
	c.showDiff(string(old), cur, "backup "+b.ID, "current")
	return nil
}

func (c *Ctx) cmdRestore(args []string) error {
	a, err := parseArgs(args)
	if err != nil {
		return err
	}
	ref := "latest"
	if len(a.pos) > 0 {
		ref = a.pos[0]
	}
	b, err := findBackup(ref)
	if err != nil {
		return err
	}
	text, err := os.ReadFile(b.File)
	if err != nil {
		return err
	}
	c.pf("Restoring backup %s (%s)\n\n", b.ID, b.Note)
	return c.writeFstab(string(text), "restore of "+b.ID, nil)
}

// ---- import -----------------------------------------------------------

type bundleDevice struct {
	Path     string `json:"path"`
	Size     string `json:"size"`
	Fstype   string `json:"fstype"`
	Label    string `json:"label"`
	UUID     string `json:"uuid"`
	PartUUID string `json:"partuuid"`
	Model    string `json:"model"`
}

type LinkRef struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

type Bundle struct {
	Tool     string            `json:"tool"`
	Version  string            `json:"version"`
	Exported string            `json:"exported"`
	Host     string            `json:"host"`
	Distro   string            `json:"distro"`
	Kernel   string            `json:"kernel"`
	Entries  []json.RawMessage `json:"entries"`
	Devices  []bundleDevice    `json:"devices"`
	Home     string            `json:"home"`
	Links    []LinkRef         `json:"links"`
	Raw      string            `json:"raw_fstab"`

	KeyFolders   []string `json:"key_folders,omitempty"`
	TrackedLinks []string `json:"tracked_links,omitempty"`
}

func readBundle(path string) (*Bundle, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fail("cannot read bundle: %v", err)
	}
	var bd Bundle
	if err := json.Unmarshal(b, &bd); err != nil {
		return nil, fail("cannot read bundle: %v", err)
	}
	if bd.Tool != "fstabctl" {
		return nil, fail("not an fstabctl bundle (create one with: fstabctl export)")
	}
	return &bd, nil
}

func (c *Ctx) cmdImport(args []string) error {
	a, err := parseArgs(args, "include-system", "skip-missing")
	if err != nil {
		return err
	}
	if len(a.pos) != 1 {
		return fail("import needs a bundle file")
	}
	bd, err := readBundle(a.pos[0])
	if err != nil {
		return err
	}
	fs, err := loadFstab()
	if err != nil {
		return err
	}
	curMP, curSpecs := map[string]bool{}, map[string]bool{}
	for _, ie := range fs.Entries() {
		if ie.E.Enabled {
			curMP[ie.E.File] = true
		}
		curSpecs[ie.E.Spec] = true
	}
	devs := lsblk()
	oldByUUID := map[string]bundleDevice{}
	for _, d := range bd.Devices {
		if d.UUID != "" {
			oldByUUID[d.UUID] = d
		}
	}
	type row struct {
		action string
		e      *Entry
		why    string
	}
	var plan []row
	var toAdd []*Entry
	var mkdirs []string
	for _, raw := range bd.Entries {
		e := &Entry{Options: "defaults", Enabled: true}
		if json.Unmarshal(raw, e) != nil {
			continue
		}
		if systemMounts[e.File] && !a.has("include-system") {
			plan = append(plan, row{"skip", e, "system mount (use --include-system to import)"})
			continue
		}
		if e.Fstype != "swap" && curMP[e.File] {
			plan = append(plan, row{"skip", e, "mount point already in fstab"})
			continue
		}
		if e.Fstype == "swap" && curSpecs[e.Spec] {
			plan = append(plan, row{"skip", e, "swap already configured"})
			continue
		}
		if resolve(e.Spec) == "" {
			label := ""
			if m := regexp.MustCompile(`^(UUID|PARTUUID)=(.*)$`).FindStringSubmatch(e.Spec); m != nil {
				label = oldByUUID[m[2]].Label
			} else if strings.HasPrefix(e.Spec, "LABEL=") {
				label = e.Spec[6:]
			}
			var matches []Device
			for _, d := range devs {
				if label != "" && d.Label == label && d.Fstype != "" {
					matches = append(matches, d)
				}
			}
			switch {
			case len(matches) == 1 && matches[0].UUID != "":
				e.Spec = "UUID=" + matches[0].UUID
				plan = append(plan, row{"add", e, fmt.Sprintf("re-linked by label '%s' -> %s", label, matches[0].Path)})
			case a.has("skip-missing"):
				plan = append(plan, row{"skip", e, "device not present on this system"})
				continue
			default:
				e.Enabled = false
				plan = append(plan, row{"add-disabled", e, "device not present; kept as disabled entry"})
			}
		} else {
			plan = append(plan, row{"add", e, "device found"})
		}
		toAdd = append(toAdd, e)
		if e.Enabled && strings.HasPrefix(e.File, "/") && e.Fstype != "swap" {
			mkdirs = append(mkdirs, e.File)
		}
	}
	var rows [][]string
	for _, r := range plan {
		rows = append(rows, []string{r.action, r.e.File, r.e.Spec, r.why})
	}
	c.pl(table([]string{"ACTION", "MOUNT POINT", "DEVICE", "WHY"}, rows, false))
	c.pl()
	if len(toAdd) == 0 {
		c.pl("Nothing to import.")
		return nil
	}
	for _, e := range toAdd {
		fs.Append(e)
	}
	return c.writeFstab(fs.Text(), "import of "+filepath.Base(a.pos[0]), func() {
		for _, d := range mkdirs {
			if !isDir(d) && os.MkdirAll(d, 0o755) == nil {
				c.pf("Created mount point %s\n", d)
			}
		}
		if len(bd.Links) > 0 {
			c.pf("This bundle also has %d home-folder link(s). Once your disks are mounted, import them with the Home links tab.\n", len(bd.Links))
		}
	})
}

// findExpected resolves the single positional target and, with --expect SPEC,
// refuses if that entry no longer has that device spec (fstab changed since the
// caller looked at it, so a numbered target could now mean another line).
func findExpected(fs *Fstab, a *argset) (IdxEntry, error) {
	ie, err := findTarget(fs, a.pos[0])
	if err != nil {
		return ie, err
	}
	if a.has("expect") && ie.E.Spec != a.str("expect") {
		return ie, fail("fstab has changed since this was shown (entry %s is now %s); refresh and try again", a.pos[0], ie.E.Spec)
	}
	return ie, nil
}

// cmdMount mounts one entry, or with --all every enabled entry that should be
// mounted but isn't, so changes take effect without a reboot.
func (c *Ctx) cmdMount(args []string) error {
	a, err := parseArgs(args, "all", "expect=")
	if err != nil {
		return err
	}
	if a.has("all") == (len(a.pos) == 1) {
		return fail("mount needs one target, or --all")
	}
	fs, err := loadFstab()
	if err != nil {
		return err
	}
	var cands []IdxEntry
	if a.has("all") {
		cands = fs.Entries()
	} else {
		ie, err := findExpected(fs, a)
		if err != nil {
			return err
		}
		cands = []IdxEntry{ie}
	}
	mounted := mountedPoints()
	var plan []*Entry
	var skipped [][2]string
	for _, ie := range cands {
		e := ie.E
		reason := ""
		switch {
		case !e.Enabled:
			reason = "disabled"
		case e.Fstype == "swap" || e.File == "none" || e.File == "swap":
			reason = "swap, not a folder"
		case mounted[e.File]:
			reason = "already mounted"
		case systemMounts[e.File]:
			reason = "boot-critical; the system mounts it"
		case a.has("all") && (e.HasOpt("noauto") || e.HasOpt("x-systemd.automount")):
			reason = "mounts by itself when you open the folder"
		case resolve(e.Spec) == "":
			reason = "disk not connected"
		}
		if reason != "" {
			skipped = append(skipped, [2]string{e.File, reason})
			if !a.has("all") {
				return fail("can't mount %s: %s", nameOf(e), reason)
			}
			continue
		}
		plan = append(plan, e)
	}
	if len(plan) == 0 {
		c.pl("Nothing to do: every entry that should be mounted already is.")
		for _, s := range skipped {
			c.pf("  skipped %s (%s)\n", nameOf(&Entry{File: s[0]}), s[1])
		}
		return nil
	}
	c.pl("Will mount:")
	for _, e := range plan {
		c.pf("  %s  (%s, %s)\n", e.File, e.Spec, e.Fstype)
	}
	if len(skipped) > 0 {
		c.pl("Skipping:")
		for _, s := range skipped {
			c.pf("  %s  (%s)\n", s[0], s[1])
		}
	}
	if !c.apply {
		c.pl("\n(dry run: nothing mounted)")
		return nil
	}
	if os.Geteuid() != 0 {
		return fail("mounting needs administrator rights; re-run with sudo")
	}
	if have("systemctl") && isDir("/run/systemd/system") {
		runCmd("systemctl", "daemon-reload") // so systemd's mount units match the current fstab
	}
	c.pl("")
	failed := 0
	for _, e := range plan {
		if !pathExists(e.File) {
			os.MkdirAll(e.File, 0o755)
		}
		margs := []string{e.File}
		if fstabPath() != "/etc/fstab" {
			margs = []string{"--fstab", fstabPath(), e.File}
		}
		o, er, code := runCmd("mount", margs...)
		if code != 0 {
			failed++
			msg := strings.TrimSpace(er + " " + o)
			if msg == "" {
				msg = fmt.Sprintf("mount exited with code %d", code)
			}
			c.pf("✗ %s: %s\n", e.File, msg)
		} else {
			c.pf("✓ mounted %s\n", e.File)
		}
	}
	if failed > 0 {
		return &exitErr{1, fmt.Sprintf("%d of %d mounts failed", failed, len(plan))}
	}
	return nil
}

func nameOf(e *Entry) string {
	if e.File == "" || e.File == "none" || e.File == "swap" {
		return e.Spec
	}
	return e.File
}

// cmdRepair fixes a line that can't be parsed: --comment-out N puts a # in front
// of it; --add-defaults N completes a 3-field line with standard options.
func (c *Ctx) cmdRepair(args []string) error {
	a, err := parseArgs(args, "comment-out=", "add-defaults=", "expect=")
	if err != nil {
		return err
	}
	mode, val := "comment-out", a.str("comment-out")
	if a.has("add-defaults") {
		mode, val = "add-defaults", a.str("add-defaults")
	}
	if a.has("comment-out") == a.has("add-defaults") || len(a.pos) != 0 {
		return fail("repair needs exactly one of --comment-out LINE or --add-defaults LINE")
	}
	n, err := strconv.Atoi(val)
	fs, ferr := loadFstab()
	if ferr != nil {
		return ferr
	}
	if err != nil || n < 1 || n > len(fs.Lines) {
		return fail("no line %s in %s", val, fstabPath())
	}
	raw := fs.Lines[n-1]
	line := strings.TrimSpace(raw)
	if a.has("expect") && line != a.str("expect") {
		return fail("fstab has changed since this was shown (line %d is now: %s); refresh and try again", n, line)
	}
	if _, perr := parseLine(raw); perr == nil {
		return fail("line %d isn't broken; nothing to repair", n)
	}
	switch mode {
	case "comment-out":
		fs.Lines[n-1] = "# " + line + "    # commented out by fstab Manager: this line could not be read"
	case "add-defaults":
		p := strings.Fields(line)
		if len(p) != 3 || !knownFS[p[2]] {
			return fail("line %d can't be completed automatically; comment it out instead", n)
		}
		opts := "defaults"
		if !systemMounts[unescape(p[1])] && p[2] != "swap" {
			opts += ",nofail"
		}
		if p[2] == "nfs" || p[2] == "nfs4" || p[2] == "cifs" {
			opts += ",_netdev"
		}
		fs.Lines[n-1] = strings.Join(p, " ") + " " + opts + " 0 0"
	}
	return c.writeFstab(fs.Text(), fmt.Sprintf("repair line %d", n), nil)
}
