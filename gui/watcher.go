package main

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"
)

// DeviceEvent is pushed to the page (Server-Sent Events) when hardware or
// mount state changes. Type is "devices" (disks appeared/vanished) or
// "refresh" (something else changed, e.g. a mount).
type DeviceEvent struct {
	Type    string   `json:"type"`
	Added   []Device `json:"added"`
	Removed []Device `json:"removed"`
}

type hub struct {
	mu   sync.Mutex
	subs map[chan DeviceEvent]struct{}
}

func newHub() *hub { return &hub{subs: map[chan DeviceEvent]struct{}{}} }

func (h *hub) subscribe() chan DeviceEvent {
	ch := make(chan DeviceEvent, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan DeviceEvent) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *hub) broadcast(ev DeviceEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default: // slow consumer: drop rather than block the watcher
		}
	}
}

// hwSignature is a cheap fingerprint of "something about disks changed":
// the kernel partition table, by-uuid symlinks (appear after mkfs / on insert)
// and the mount table. No subprocesses, so it is fine to poll every second or two.
func hwSignature() string {
	var sb strings.Builder
	for _, p := range []string{"/proc/partitions", "/proc/self/mounts"} {
		if b, err := os.ReadFile(p); err == nil {
			sb.Write(b)
		}
	}
	if ents, err := os.ReadDir("/dev/disk/by-uuid"); err == nil {
		for _, e := range ents {
			sb.WriteString(e.Name())
			sb.WriteByte('\n')
		}
	}
	if p := os.Getenv("FSTABCTL_LSBLK_JSON"); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			sb.Write(b)
		}
	}
	return sha(sb.String())
}

func devKey(d Device) string { return d.Path + "|" + d.UUID + "|" + d.Fstype }

// diffDevices reports devices that appeared or went away. A device whose
// filesystem changed (e.g. just formatted) counts as added, not removed.
func diffDevices(old, cur []Device) (added, removed []Device) {
	oldK, curK := map[string]bool{}, map[string]bool{}
	for _, d := range old {
		oldK[devKey(d)] = true
	}
	for _, d := range cur {
		curK[devKey(d)] = true
	}
	addedPaths := map[string]bool{}
	for _, d := range cur {
		if !oldK[devKey(d)] {
			added = append(added, d)
			addedPaths[d.Path] = true
		}
	}
	for _, d := range old {
		if !curK[devKey(d)] && !addedPaths[d.Path] {
			removed = append(removed, d)
		}
	}
	return
}

// specMatches reports whether an fstab spec refers to this device, by identity
// rather than by resolving symlinks (which may lag right after plug-in/removal).
func specMatches(spec string, d Device) bool {
	if m := keyRe.FindStringSubmatch(spec); m != nil {
		switch m[1] {
		case "UUID":
			return d.UUID != "" && strings.EqualFold(d.UUID, m[2])
		case "LABEL":
			return d.Label != "" && d.Label == m[2]
		case "PARTUUID":
			return d.PartUUID != "" && strings.EqualFold(d.PartUUID, m[2])
		}
		return false
	}
	return spec == d.Path
}

func annotate(devs []Device) []Device {
	ents := currentEntriesQuiet()
	for i := range devs {
		devs[i].Candidate = isCandidate(&devs[i])
		devs[i].InFstab = false
		for _, ie := range ents {
			if ie.E.Enabled && specMatches(ie.E.Spec, devs[i]) {
				devs[i].InFstab = true
				break
			}
		}
		if devs[i].Mounts == nil {
			devs[i].Mounts = []string{}
		}
	}
	return devs
}

// watch polls sig(); when it changes (and stays stable for `settle`, to let udev
// finish) it re-lists devices and broadcasts what was added/removed.
func watch(ctx context.Context, h *hub, interval, settle time.Duration,
	sig func() string, list func() []Device, onEvent func(DeviceEvent)) {
	prevDevs := list()
	prevSig := sig()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s := sig()
		if s == prevSig {
			continue
		}
		for { // wait for the signature to stop changing
			select {
			case <-ctx.Done():
				return
			case <-time.After(settle):
			}
			s2 := sig()
			if s2 == s {
				break
			}
			s = s2
		}
		prevSig = s
		cur := list()
		added, removed := diffDevices(prevDevs, cur)
		prevDevs = cur
		ev := DeviceEvent{Type: "refresh", Added: []Device{}, Removed: []Device{}}
		if len(added)+len(removed) > 0 {
			ev = DeviceEvent{Type: "devices", Added: annotate(added), Removed: annotate(removed)}
			if ev.Added == nil {
				ev.Added = []Device{}
			}
			if ev.Removed == nil {
				ev.Removed = []Device{}
			}
		}
		h.broadcast(ev)
		if onEvent != nil {
			onEvent(ev)
		}
	}
}

// notifyDesktop shows a native notification if notify-send exists (best effort).
func notifyDesktop(ev DeviceEvent) {
	if ev.Type != "devices" || !have("notify-send") {
		return
	}
	for _, d := range ev.Added {
		if !d.Candidate {
			continue
		}
		title := "New disk: " + d.Path
		body := d.Fstype + " · " + d.Size
		if d.Label != "" {
			body += " · " + d.Label
		}
		if d.InFstab {
			body += " (already in fstab)"
		} else {
			body += " – open fstab Manager to add it to fstab"
		}
		launch("notify-send", "-a", "fstab Manager", "-i", "fstab-manager", title, body)
	}
}
