package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// FindingAction is a one-click fix: the arguments of an operation the UI
// previews (as a diff) before anything is applied.
type FindingAction struct {
	Label string   `json:"label"`
	Args  []string `json:"args"`
}

// Finding is one item in the Overview's Findings list: what is wrong, why it
// matters, what to do about it, and buttons that do it.
type Finding struct {
	Level   string          `json:"level"` // error, warn, info
	Text    string          `json:"text"`
	Why     string          `json:"why,omitempty"`
	Fix     string          `json:"fix,omitempty"`
	Actions []FindingAction `json:"actions"`
}

var optHelp = map[string]string{
	"defaults": "rw, suid, dev, exec, auto, nouser, async",
	"nofail":   "boot continues if the device is absent",
	"noauto":   "not mounted at boot (mount manually or via automount)",
	"auto":     "mounted at boot / by 'mount -a'",
	"ro":       "read-only", "rw": "read-write",
	"noatime":  "do not update file access times (fewer writes)",
	"relatime": "update access time only when older than modify time",
	"nosuid":   "ignore setuid/setgid bits", "nodev": "ignore device files",
	"noexec": "forbid executing binaries", "user": "any user may mount it",
	"users":               "any user may mount and unmount it",
	"discard":             "continuous TRIM (consider fstrim.timer instead)",
	"_netdev":             "needs the network up before mounting",
	"x-systemd.automount": "mount lazily on first access",
	"sw":                  "swap space", "bind": "bind mount of another directory",
}

var prefixHelp = [][2]string{
	{"uid=", "files appear owned by this user id"}, {"gid=", "files appear owned by this group id"},
	{"umask=", "permission mask for all files"}, {"fmask=", "permission mask for files"},
	{"dmask=", "permission mask for directories"}, {"compress=", "transparent compression (btrfs)"},
	{"subvol=", "mounts this btrfs subvolume"}, {"subvolid=", "mounts this btrfs subvolume id"},
	{"credentials=", "file holding network share username/password"},
	{"x-systemd.device-timeout=", "how long systemd waits for the device"},
	{"x-systemd.idle-timeout=", "unmount after this much idle time"},
	{"x-systemd.mount-timeout=", "how long systemd waits for the mount"},
	{"errors=", "action on filesystem error"}, {"pri=", "swap priority"},
	{"vers=", "network protocol version"}, {"commit=", "journal commit interval (s)"},
	{"size=", "maximum size"}, {"mode=", "permissions of the mount root"},
}

func explain(opt string) string {
	if t, ok := optHelp[opt]; ok {
		return t
	}
	for _, p := range prefixHelp {
		if strings.HasPrefix(opt, p[0]) {
			return p[1] + " (" + opt[len(p[0]):] + ")"
		}
	}
	return ""
}

var kernelDevRe = regexp.MustCompile(`^/dev/(sd|vd|hd|nvme|mmcblk)`)

// guarded builds the arguments for an operation on entry number n, adding
// --expect so the fix is refused if fstab changed since it was shown.
func guarded(verb string, n int, spec string, extra ...string) []string {
	return append([]string{verb, strconv.Itoa(n), "--expect", spec}, extra...)
}

// analyze returns advice about the enabled entries. ents are numbered as in the
// entry list (1-based), which is what the fix buttons refer to.
func analyze(ents []IdxEntry, devs []Device) []Finding {
	var out []Finding
	mounted := mountedPoints()
	for i, ie := range ents {
		e, n := ie.E, i+1
		if !e.Enabled {
			continue
		}
		name := e.File
		if name == "none" || name == "swap" {
			name = e.Spec
		}
		r := resolve(e.Spec)
		network := r == "network"
		local := r != "network" && r != "pseudo" && e.Fstype != "swap"
		nofail := e.HasOpt("nofail") || e.HasOpt("noauto")
		advisedNofail := false

		switch {
		case r == "" && systemMounts[e.File]:
			out = append(out, Finding{Level: "error", Actions: []FindingAction{},
				Text: fmt.Sprintf("The disk for %s (%s) can't be found", e.File, e.Spec),
				Why: "This is a boot-critical mount. If the entry is wrong (for example a mistyped UUID), the next " +
					"boot will stop in an emergency shell.",
				Fix: "Don't reboot yet. Compare the UUID with the Disks tab (or “lsblk -f”) and correct it with Edit, " +
					"or restore a recent backup from the Backups tab."})
		case r == "" && !nofail:
			advisedNofail = true
			out = append(out, Finding{Level: "warn",
				Text: fmt.Sprintf("%s: the disk (%s) isn't connected, and this entry has no “nofail”", name, e.Spec),
				Why: "At boot, systemd waits about 90 seconds for this disk. Without “nofail” it then stops in an " +
					"emergency shell instead of finishing startup.",
				Fix: "Add “nofail” so startup carries on without the disk, or disable the entry if the disk is gone for good.",
				Actions: []FindingAction{
					{"Add nofail", guarded("set", n, e.Spec, "--add-options", "nofail")},
					{"Disable entry", guarded("disable", n, e.Spec)},
				}})
		case r == "":
			out = append(out, Finding{Level: "info",
				Text: fmt.Sprintf("%s: the disk (%s) isn't connected right now", name, e.Spec),
				Why:  "This entry has “nofail”, so startup won't wait for it. It mounts when the disk is present at boot.",
				Fix:  "Nothing to do if the disk is just unplugged. Disable the entry if the disk is gone for good.",
				Actions: []FindingAction{
					{"Disable entry", guarded("disable", n, e.Spec)},
				}})
		}

		if r != "" && e.Fstype != "swap" && e.File != "none" && !mounted[e.File] && !systemMounts[e.File] &&
			!e.HasOpt("noauto") && !e.HasOpt("x-systemd.automount") {
			out = append(out, Finding{Level: "info",
				Text: fmt.Sprintf("%s is in fstab but not mounted right now", e.File),
				Why: "Changes to fstab take effect at the next boot. Until then (or if mounting failed), the folder " +
					"is empty and anything saved there goes to your system disk instead.",
				Fix: "Mount it now. If it fails, the message explains why (for example a wrong filesystem type).",
				Actions: []FindingAction{
					{"Mount now", guarded("mount", n, e.Spec)},
				}})
		}

		if network && !(e.HasOpt("_netdev") || e.HasOpt("x-systemd.automount")) {
			out = append(out, Finding{Level: "warn",
				Text: fmt.Sprintf("%s: network share without “_netdev”", name),
				Why:  "Without it, the system may try to mount the share before the network is up, which fails or delays boot.",
				Fix:  "Add “_netdev” so it waits for the network.",
				Actions: []FindingAction{
					{"Add _netdev", guarded("set", n, e.Spec, "--add-options", "_netdev")},
				}})
		}

		if local && !advisedNofail && !systemMounts[e.File] && !nofail && e.Fstype != "tmpfs" && r != "" {
			out = append(out, Finding{Level: "info",
				Text: fmt.Sprintf("%s: no “nofail”", name),
				Why: "It's fine while the disk is connected. If the disk ever fails or is unplugged, the machine " +
					"will stop at boot instead of starting without it.",
				Fix: "Add “nofail”. It changes nothing while the disk is present.",
				Actions: []FindingAction{
					{"Add nofail", guarded("set", n, e.Spec, "--add-options", "nofail")},
				}})
		}

		if e.HasOpt("discard") {
			out = append(out, Finding{Level: "info",
				Text: fmt.Sprintf("%s: uses “discard” (continuous TRIM)", name),
				Why:  "Trimming on every delete can slow some SSDs. Most distributions run a weekly trim (fstrim.timer) instead.",
				Fix:  "Remove “discard” and make sure fstrim.timer is enabled (systemctl status fstrim.timer).",
				Actions: []FindingAction{
					{"Remove discard", guarded("set", n, e.Spec, "--remove-options", "discard")},
				}})
		}

		for _, o := range e.Opts() {
			if !strings.HasPrefix(o, "credentials=") {
				continue
			}
			p := strings.TrimPrefix(o, "credentials=")
			if st, err := os.Stat(p); err != nil {
				out = append(out, Finding{Level: "warn", Actions: []FindingAction{},
					Text: fmt.Sprintf("%s: credentials file %s doesn't exist", name, p),
					Why:  "The share needs a username and password from this file, so mounting will fail.",
					Fix:  fmt.Sprintf("Create it with lines “username=…” and “password=…”, then run: sudo chmod 600 %s", p)})
			} else if st.Mode().Perm()&0o077 != 0 {
				out = append(out, Finding{Level: "warn", Actions: []FindingAction{},
					Text: fmt.Sprintf("%s: credentials file %s can be read by other users", name, p),
					Why:  "It holds a password in plain text.",
					Fix:  fmt.Sprintf("Run: sudo chmod 600 %s", p)})
			}
		}

		if kernelDevRe.MatchString(e.Spec) && e.File != "none" {
			f := Finding{Level: "info", Actions: []FindingAction{},
				Text: fmt.Sprintf("%s: refers to the disk as %s", name, e.Spec),
				Why: "Names like /dev/sdb1 depend on the order disks are detected, which can change when you add or " +
					"remove a disk. The wrong disk could then be mounted here.",
				Fix: "Refer to it by UUID, which never changes."}
			for _, d := range devs {
				if d.UUID != "" && (d.Path == e.Spec || (r != "" && realpath(d.Path) == r)) {
					f.Fix = fmt.Sprintf("Refer to it by UUID (UUID=%s), which never changes.", d.UUID)
					f.Actions = []FindingAction{{"Switch to UUID", guarded("set", n, e.Spec, "--device", "UUID="+d.UUID)}}
					break
				}
			}
			out = append(out, f)
		}
	}
	return out
}

var knownFS = map[string]bool{"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "vfat": true,
	"exfat": true, "ntfs": true, "ntfs3": true, "ntfs-3g": true, "f2fs": true, "nfs": true, "nfs4": true,
	"cifs": true, "swap": true, "tmpfs": true, "zfs": true, "jfs": true, "reiserfs": true, "hfsplus": true}

// validateFindings explains the blocking problems in an fstab text (the same
// ones validate() reports) and offers repairs.
func validateFindings(text string) []Finding {
	f := parseFstab(text)
	out := []Finding{}
	for i, raw := range f.Lines {
		_, err := parseLine(raw)
		if err == nil {
			continue
		}
		line := strings.TrimSpace(raw)
		ln := strconv.Itoa(i + 1)
		fd := Finding{Level: "error",
			Text: fmt.Sprintf("Line %d can't be read (%v): %s", i+1, err, line),
			Why: "Each line needs 4 to 6 fields separated by spaces: device, mount point, filesystem type, options, " +
				"and optionally two numbers. A line that can't be read is ignored or makes the mount fail at boot.",
			Fix: "Comment it out (the text is kept with a # in front, and the system ignores it), then add the mount " +
				"again with “Add mount…”. Or fix the line by hand.",
			Actions: []FindingAction{{"Comment out line " + ln, []string{"repair", "--comment-out", ln, "--expect", line}}}}
		if p := strings.Fields(line); len(p) == 3 && knownFS[p[2]] {
			fd.Fix = "It's missing its options. Add the standard ones (“defaults”, plus “nofail” for non-system disks), " +
				"or comment the line out."
			fd.Actions = append([]FindingAction{{"Add default options", []string{"repair", "--add-defaults", ln, "--expect", line}}}, fd.Actions...)
		}
		out = append(out, fd)
	}
	seen := map[string]int{}
	ents := f.Entries()
	for i, ie := range ents {
		e := ie.E
		if !e.Enabled {
			continue
		}
		if !strings.HasPrefix(e.File, "/") && e.File != "none" && e.File != "swap" {
			out = append(out, Finding{Level: "error",
				Text:    fmt.Sprintf("%s: mount point “%s” isn't a full path", e.Spec, e.File),
				Why:     "Mount points must start with / (for example /mnt/data). This entry can't be mounted.",
				Fix:     "Edit the entry and enter the full path, or disable it.",
				Actions: []FindingAction{{"Disable entry", guarded("disable", i+1, e.Spec)}}})
		}
		if e.Fstype != "swap" && e.File != "none" {
			if prev, ok := seen[e.File]; ok {
				out = append(out, Finding{Level: "error",
					Text: fmt.Sprintf("Two entries mount at %s (%s and %s)", e.File, ents[prev-1].E.Spec, e.Spec),
					Why:  "Only the last one would be visible; the other disk is hidden underneath it.",
					Fix:  "Disable one of them, or edit one to use a different folder.",
					Actions: []FindingAction{
						{"Disable " + shortSpec(e.Spec), guarded("disable", i+1, e.Spec)},
						{"Disable " + shortSpec(ents[prev-1].E.Spec), guarded("disable", prev, ents[prev-1].E.Spec)},
					}})
			}
			seen[e.File] = i + 1
		}
	}
	return out
}

func shortSpec(s string) string {
	if len(s) > 22 {
		return s[:20] + "…"
	}
	return s
}
