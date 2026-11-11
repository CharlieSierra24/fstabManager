package main

import (
	"fmt"
	"strings"
	"time"
)

type ExportBundle struct {
	Tool     string         `json:"tool"`
	Version  string         `json:"version"`
	Exported string         `json:"exported"`
	Host     string         `json:"host"`
	Distro   string         `json:"distro"`
	Kernel   string         `json:"kernel"`
	Entries  []Entry        `json:"entries"`
	Devices  []bundleDevice `json:"devices"`
	Home     string         `json:"home"`
	Links    []LinkRef      `json:"links"`
	Raw      string         `json:"raw_fstab"`

	KeyFolders   []string `json:"key_folders"`
	TrackedLinks []string `json:"tracked_links"`
}

func buildBundle(hidden bool, depth int) (*ExportBundle, error) {
	text, err := readFstab()
	if err != nil {
		return nil, err
	}
	fs := parseFstab(text)
	ents := fs.Entries()
	b := &ExportBundle{Tool: "fstabctl", Version: "gui-" + version, Exported: time.Now().Format("2006-01-02T15:04:05"),
		Host: hostname(), Distro: distro(), Kernel: kernelRelease(), Entries: []Entry{}, Devices: []bundleDevice{},
		Home: homeDir(), Links: []LinkRef{}, Raw: fs.Text()}
	for _, ie := range ents {
		b.Entries = append(b.Entries, *ie.E)
	}
	for _, d := range lsblk() {
		if d.Fstype != "" {
			b.Devices = append(b.Devices, bundleDevice{d.Path, d.Size, d.Fstype, d.Label, d.UUID, d.PartUUID, d.Model})
		}
	}
	for _, l := range managedLinks(b.Home, ents, mountedPoints()) {
		b.Links = append(b.Links, LinkRef{l.Name, l.Target})
	}
	s := loadSettings()
	b.KeyFolders, b.TrackedLinks = s.KeyFolders, s.TrackedLinks
	return b, nil
}

func buildReport(format string, hidden bool, depth int) (string, error) {
	md := format == "markdown"
	text, err := readFstab()
	if err != nil {
		return "", err
	}
	fs := parseFstab(text)
	ents := fs.Entries()
	mounted, swaps := mountedPoints(), swapDevices()
	devs := lsblk()
	for i := range devs {
		devs[i].InFstab = devs[i].Candidate && inFstab(&devs[i], ents)
	}
	var out []string
	h := func(title string, level int) {
		if md {
			out = append(out, strings.Repeat("#", level)+" "+title, "")
		} else {
			ch := "-"
			if level == 1 {
				ch = "="
			}
			out = append(out, title, strings.Repeat(ch, len(title)), "")
		}
	}
	h("fstab report for "+hostname(), 1)
	meta := [][2]string{{"Generated", time.Now().Format("2006-01-02 15:04")}, {"Distribution", distro()},
		{"Kernel", kernelRelease()}, {"fstab file", fstabPath()}, {"Tool", "fstab Manager " + version}}
	for _, m := range meta {
		if md {
			out = append(out, fmt.Sprintf("- **%s:** %s", m[0], m[1]))
		} else {
			out = append(out, fmt.Sprintf("%-13s: %s", m[0], m[1]))
		}
	}
	out = append(out, "")

	statuses := make([]string, len(ents))
	enabled, disabled, up, missing := 0, 0, 0, 0
	for i, ie := range ents {
		statuses[i] = statusOf(ie.E, mounted, swaps)
		if ie.E.Enabled {
			enabled++
		} else {
			disabled++
		}
		switch statuses[i] {
		case "mounted", "active":
			up++
		case "device missing":
			missing++
		}
	}
	h("Summary", 2)
	out = append(out, fmt.Sprintf("%d entries: %d enabled, %d disabled; %d currently mounted/active; %d with a missing device.",
		len(ents), enabled, disabled, up, missing), "")

	h("Configured mounts", 2)
	var rows [][]string
	for i, ie := range ents {
		e := ie.E
		r := resolve(e.Spec)
		if r == "" {
			r = "-"
		}
		rows = append(rows, []string{fmt.Sprint(i + 1), statuses[i], e.Spec, r, e.File, e.Fstype, e.Options,
			fmt.Sprintf("%d/%d", e.Dump, e.Pass)})
	}
	out = append(out, table([]string{"#", "Status", "Device", "Resolves to", "Mount point", "Type", "Options", "Dump/Pass"}, rows, md), "")

	h("What each entry does", 2)
	for i, ie := range ents {
		e := ie.E
		state := ""
		if !e.Enabled {
			state = " (DISABLED)"
		}
		out = append(out, fmt.Sprintf("%d. %s%s: %s filesystem on %s", i+1, e.File, state, e.Fstype, e.Spec))
		for _, o := range e.Opts() {
			if ex := explain(o); ex != "" {
				out = append(out, "     - "+o+": "+ex)
			} else {
				out = append(out, "     - "+o)
			}
		}
		if e.Pass != 0 {
			out = append(out, fmt.Sprintf("     - fsck order %d at boot", e.Pass))
		}
	}
	out = append(out, "")

	h("Findings", 2)
	finds := append(validateFindings(text), analyze(ents, devs)...)
	if len(finds) > 0 {
		for _, f := range finds {
			out = append(out, fmt.Sprintf("- [%s] %s", strings.ToUpper(f.Level), f.Text))
			if f.Why != "" {
				out = append(out, "  Why it matters: "+f.Why)
			}
			if f.Fix != "" {
				out = append(out, "  What to do: "+f.Fix)
			}
		}
	} else {
		out = append(out, "No problems found.")
	}
	out = append(out, "")

	home := homeDir()
	if links := currentLinks(home, hidden, depth, ents, mounted); len(links) > 0 {
		h("Symlinks in "+home, 2)
		var lr [][]string
		for _, l := range links {
			st := "ok"
			if !l.OK {
				st = "BROKEN"
			}
			lr = append(lr, []string{l.Name, l.Target, st, l.Note})
		}
		out = append(out, table([]string{"Link", "Target", "Status", "Note"}, lr, md), "")
	}

	if len(devs) > 0 {
		h("Block devices", 2)
		var dr [][]string
		for _, d := range devs {
			f := "-"
			if d.Candidate {
				f = "no"
				if d.InFstab {
					f = "yes"
				}
			}
			dr = append(dr, []string{d.Path, d.Size, d.Type, d.Fstype, d.Label, d.UUID, strings.Join(d.Mounts, ","), f})
		}
		out = append(out, table([]string{"Device", "Size", "Type", "FS", "Label", "UUID", "Mounted at", "In fstab"}, dr, md), "")
		var free []Device
		for _, d := range devs {
			if d.Candidate && len(d.Mounts) == 0 && !d.InFstab {
				free = append(free, d)
			}
		}
		if len(free) > 0 {
			h("Formatted devices not in fstab", 2)
			for _, d := range free {
				lbl := d.Label
				if lbl == "" {
					lbl = "-"
				}
				out = append(out, fmt.Sprintf("- %s (%s, %s, label: %s)", d.Path, d.Fstype, d.Size, lbl))
			}
			out = append(out, "")
		}
	}

	h("Raw fstab", 2)
	raw := strings.Split(strings.TrimRight(fs.Text(), "\n"), "\n")
	if md {
		out = append(out, "```")
		out = append(out, raw...)
		out = append(out, "```")
	} else {
		for _, l := range raw {
			out = append(out, "    "+l)
		}
	}
	out = append(out, "")
	return strings.Join(out, "\n"), nil
}
