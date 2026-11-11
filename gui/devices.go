package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var nonFS = map[string]bool{"LVM2_member": true, "crypto_LUKS": true, "linux_raid_member": true,
	"zfs_member": true, "ceph_bluestore": true, "BitLocker": true}

var byDir = map[string]string{"UUID": "by-uuid", "LABEL": "by-label", "PARTUUID": "by-partuuid",
	"PARTLABEL": "by-partlabel", "ID": "by-id"}

var keyRe = regexp.MustCompile(`^(UUID|LABEL|PARTUUID|PARTLABEL|ID)=(.*)$`)

// Device is one lsblk row. JSON tags are what the web UI expects.
type Device struct {
	Path      string   `json:"path"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Size      string   `json:"size"`
	Fstype    string   `json:"fstype"`
	Label     string   `json:"label"`
	UUID      string   `json:"uuid"`
	PartUUID  string   `json:"partuuid"`
	Model     string   `json:"model"`
	Rota      bool     `json:"rota"`
	Rm        bool     `json:"rm"`
	Mounts    []string `json:"mounts"`
	Candidate bool     `json:"candidate"`
	InFstab   bool     `json:"in_fstab"`
}

type lsNode struct {
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Type        string    `json:"type"`
	Size        string    `json:"size"`
	Fstype      string    `json:"fstype"`
	Label       string    `json:"label"`
	UUID        string    `json:"uuid"`
	PartUUID    string    `json:"partuuid"`
	Model       string    `json:"model"`
	Rota        any       `json:"rota"`
	Rm          any       `json:"rm"`
	Mountpoint  *string   `json:"mountpoint"`
	Mountpoints []*string `json:"mountpoints"`
	Children    []lsNode  `json:"children"`
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "1" || strings.EqualFold(t, "true")
	case float64:
		return t == 1
	}
	return false
}

func lsblk() []Device {
	base := "NAME,PATH,TYPE,SIZE,FSTYPE,LABEL,UUID,PARTUUID,MODEL,ROTA,RM"
	var out string
	ok := false
	if p := os.Getenv("FSTABCTL_LSBLK_JSON"); p != "" { // testing hook: read lsblk -J output from a file
		if b, err := os.ReadFile(p); err == nil {
			out, ok = string(b), true
		}
	}
	for _, mp := range []string{"MOUNTPOINTS", "MOUNTPOINT"} {
		if ok {
			break
		}
		o, _, code := runCmd("lsblk", "-J", "-o", base+","+mp)
		if code == 0 && o != "" {
			out, ok = o, true
		}
	}
	if !ok {
		return nil
	}
	var parsed struct {
		Blockdevices []lsNode `json:"blockdevices"`
	}
	if json.Unmarshal([]byte(out), &parsed) != nil {
		return nil
	}
	var devs []Device
	var walk func(nodes []lsNode, model string)
	walk = func(nodes []lsNode, model string) {
		for _, n := range nodes {
			if n.Type == "loop" || n.Type == "rom" || strings.HasPrefix(n.Name, "zram") {
				continue
			}
			mps := []string{}
			for _, p := range n.Mountpoints {
				if p != nil && *p != "" {
					mps = append(mps, *p)
				}
			}
			if n.Mountpoints == nil && n.Mountpoint != nil && *n.Mountpoint != "" {
				mps = append(mps, *n.Mountpoint)
			}
			m := strings.TrimSpace(n.Model)
			if m == "" {
				m = model
			}
			path := n.Path
			if path == "" {
				path = "/dev/" + n.Name
			}
			devs = append(devs, Device{Path: path, Name: n.Name, Type: n.Type, Size: n.Size, Fstype: n.Fstype,
				Label: n.Label, UUID: n.UUID, PartUUID: n.PartUUID, Model: m,
				Rota: truthy(n.Rota), Rm: truthy(n.Rm), Mounts: mps})
			walk(n.Children, m)
		}
	}
	walk(parsed.Blockdevices, "")
	for i := range devs {
		devs[i].Candidate = isCandidate(&devs[i])
	}
	return devs
}

func isCandidate(d *Device) bool { return d.Fstype != "" && !nonFS[d.Fstype] }

func inFstab(d *Device, ents []IdxEntry) bool {
	real := realpath(d.Path)
	for _, ie := range ents {
		if ie.E.Enabled && resolve(ie.E.Spec) == real {
			return true
		}
	}
	return false
}

// resolve returns the real device path, "network", "pseudo", or "" if missing.
func resolve(spec string) string {
	if m := keyRe.FindStringSubmatch(spec); m != nil {
		base := filepath.Join("/dev/disk", byDir[m[1]])
		for _, cand := range []string{m[2], strings.ToUpper(m[2]), strings.ToLower(m[2]),
			strings.ReplaceAll(m[2], " ", `\x20`)} {
			p := filepath.Join(base, cand)
			if pathExists(p) {
				return realpath(p)
			}
		}
		return ""
	}
	if strings.HasPrefix(spec, "//") || (strings.Contains(spec, ":") && !strings.HasPrefix(spec, "/")) {
		return "network"
	}
	if strings.HasPrefix(spec, "/dev/") {
		if pathExists(spec) {
			return realpath(spec)
		}
		return ""
	}
	if strings.HasPrefix(spec, "/") {
		if pathExists(spec) {
			return spec
		}
		return ""
	}
	return "pseudo"
}

func mountedPoints() map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) > 1 {
			out[unescape(f[1])] = true
		}
	}
	return out
}

func swapDevices() map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile("/proc/swaps")
	if err != nil {
		return out
	}
	lines := strings.Split(string(b), "\n")
	for _, l := range lines[min(1, len(lines)):] {
		if f := strings.Fields(l); len(f) > 0 {
			out[realpath(f[0])] = true
		}
	}
	return out
}

func statusOf(e *Entry, mounted, swaps map[string]bool) string {
	if !e.Enabled {
		return "disabled"
	}
	r := resolve(e.Spec)
	if r == "" {
		return "device missing"
	}
	if e.Fstype == "swap" {
		if swaps[r] {
			return "active"
		}
		return "inactive"
	}
	if mounted[e.File] {
		return "mounted"
	}
	return "not mounted"
}
