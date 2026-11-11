package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseAndFormat(t *testing.T) {
	e, err := parseLine(`UUID=1234-ABCD  /mnt/my\040disk  exfat  defaults,nofail  0  2`)
	if err != nil || e == nil {
		t.Fatalf("parse: %v", err)
	}
	if e.File != "/mnt/my disk" || e.Pass != 2 || !e.Enabled {
		t.Fatalf("bad entry: %+v", e)
	}
	if !strings.Contains(e.Line(), `/mnt/my\040disk`) {
		t.Fatalf("space not re-escaped: %s", e.Line())
	}
	d, _ := parseLine(disabledMark + " " + e.Line())
	if d == nil || d.Enabled {
		t.Fatalf("disabled marker not honoured")
	}
	if e, err := parseLine("# comment"); e != nil || err != nil {
		t.Fatalf("comment should be ignored")
	}
	if _, err := parseLine("only three fields"); err == nil {
		t.Fatalf("expected field-count error")
	}
}

func TestValidateDuplicates(t *testing.T) {
	txt := "UUID=a /mnt/x ext4 defaults 0 2\nUUID=b /mnt/x ext4 defaults 0 2\n"
	if len(validate(txt)) != 1 {
		t.Fatalf("expected duplicate mount point error, got %v", validate(txt))
	}
	if len(validate("UUID=a /mnt/x ext4 defaults 0 2\n"+disabledMark+" UUID=b /mnt/x ext4 defaults 0 2\n")) != 0 {
		t.Fatalf("disabled duplicates must not count")
	}
}

func TestUnifiedDiff(t *testing.T) {
	a := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	b := []string{"a", "b", "c", "d", "X", "f", "g", "h", "i", "j", "k"}
	d := strings.Join(unifiedDiff(a, b, "old", "new"), "\n")
	for _, want := range []string{"--- old", "+++ new", "-e", "+X", "+k", "@@ -2,9 +2,10 @@"} {
		if !strings.Contains(d, want) {
			t.Fatalf("diff missing %q:\n%s", want, d)
		}
	}
	if len(unifiedDiff(a, a, "x", "y")) != 0 {
		t.Fatalf("identical inputs must give empty diff")
	}
}

func setup(t *testing.T, content string) string {
	dir := t.TempDir()
	f := filepath.Join(dir, "fstab")
	os.WriteFile(f, []byte(content), 0o644)
	t.Setenv("FSTABCTL_FSTAB", f)
	t.Setenv("FSTABCTL_BACKUP_DIR", filepath.Join(dir, "bk"))
	t.Setenv("FSTABCTL_HOME", filepath.Join(dir, "home"))
	os.MkdirAll(filepath.Join(dir, "home"), 0o755)
	return dir
}

func op(t *testing.T, apply bool, args ...string) (int, string) {
	var b bytes.Buffer
	c := RunOp(args, apply, &b)
	return c, b.String()
}

func TestAddPreviewApplyRestore(t *testing.T) {
	dir := setup(t, "UUID=r / ext4 defaults 0 1\n")
	code, out := op(t, false, "add", "nas:/x", filepath.Join(dir, "nas"), "-t", "nfs4", "-p", "nfs")
	if code != 0 || !strings.Contains(out, "dry run") {
		t.Fatalf("preview failed: %d %s", code, out)
	}
	if b, _ := os.ReadFile(fstabPath()); strings.Contains(string(b), "nas:/x") {
		t.Fatalf("preview must not write")
	}
	code, out = op(t, true, "add", "nas:/x", filepath.Join(dir, "nas"), "-t", "nfs4", "-p", "nfs")
	if code != 0 || !strings.Contains(out, "Written") {
		t.Fatalf("apply failed: %d %s", code, out)
	}
	if b, _ := os.ReadFile(fstabPath()); !strings.Contains(string(b), "_netdev") {
		t.Fatalf("preset options missing")
	}
	if !isDir(filepath.Join(dir, "nas")) {
		t.Fatalf("mount point not created")
	}
	if code, out = op(t, true, "add", "nas:/y", filepath.Join(dir, "nas"), "-t", "nfs4"); code == 0 {
		t.Fatalf("duplicate mount point must fail: %s", out)
	}
	if code, _ = op(t, true, "restore", "latest"); code != 0 {
		t.Fatalf("restore failed")
	}
	if b, _ := os.ReadFile(fstabPath()); strings.Contains(string(b), "nas:/x") {
		t.Fatalf("restore did not revert")
	}
}

func TestSystemMountProtected(t *testing.T) {
	setup(t, "UUID=r / ext4 defaults 0 1\n")
	if code, out := op(t, true, "remove", "/"); code == 0 || !strings.Contains(out, "boot-critical") {
		t.Fatalf("root removal must be refused: %s", out)
	}
	if code, _ := op(t, true, "disable", "/"); code == 0 {
		t.Fatalf("root disable must be refused")
	}
}

func TestDisableEnableSet(t *testing.T) {
	setup(t, "UUID=r / ext4 defaults 0 1\nUUID=d /mnt/d ext4 defaults 0 2\n")
	op(t, true, "disable", "/mnt/d")
	b, _ := os.ReadFile(fstabPath())
	if !strings.Contains(string(b), disabledMark) {
		t.Fatalf("not disabled")
	}
	op(t, true, "enable", "/mnt/d")
	op(t, true, "set", "/mnt/d", "--add-options", "nofail,noatime")
	b, _ = os.ReadFile(fstabPath())
	if !strings.Contains(string(b), "defaults,nofail,noatime") {
		t.Fatalf("add-options failed: %s", b)
	}
	op(t, true, "set", "/mnt/d", "--remove-options", "noatime")
	b, _ = os.ReadFile(fstabPath())
	if strings.Contains(string(b), "noatime") {
		t.Fatalf("remove-options failed")
	}
}

func TestPreexistingProblemsDoNotBlock(t *testing.T) {
	setup(t, "UUID=r / ext4 defaults 0 1\nbroken line here\n")
	if code, out := op(t, true, "add", "nas:/x", "/mnt/n", "-t", "nfs4", "--no-mkdir"); code != 0 {
		t.Fatalf("existing malformed line must not block writes: %s", out)
	}
}

func TestLinksAdoptAndSnapshot(t *testing.T) {
	dir := setup(t, "UUID=r / ext4 defaults 0 1\n")
	home := homeDir()
	os.MkdirAll(filepath.Join(home, "Documents"), 0o755)
	os.WriteFile(filepath.Join(home, "Documents", "a.txt"), []byte("hi"), 0o644)
	target := filepath.Join(dir, "data", "Documents")
	os.MkdirAll(filepath.Dir(target), 0o755)
	if code, out := op(t, true, "link", "add", target); code == 0 {
		t.Fatalf("real directory must be protected without --adopt: %s", out)
	}
	if code, out := op(t, true, "link", "add", target, "--adopt"); code != 0 {
		t.Fatalf("adopt failed: %s", out)
	}
	if !isSymlink(filepath.Join(home, "Documents")) {
		t.Fatalf("link not created")
	}
	if b, _ := os.ReadFile(filepath.Join(target, "a.txt")); string(b) != "hi" {
		t.Fatalf("data not moved")
	}
	// remove only touches symlinks
	os.MkdirAll(filepath.Join(home, "Real"), 0o755)
	if code, _ := op(t, true, "link", "remove", "Real"); code == 0 {
		t.Fatalf("must refuse to remove real dir")
	}
	if code, _ := op(t, true, "link", "remove", "Documents"); code != 0 || lexists(filepath.Join(home, "Documents")) {
		t.Fatalf("link remove failed")
	}
	if code, out := op(t, true, "link", "restore"); code != 0 || !isSymlink(filepath.Join(home, "Documents")) {
		t.Fatalf("snapshot restore failed: %s", out)
	}
	// path escape
	if code, _ := op(t, true, "link", "add", "/etc/hosts", "../escape", "--allow-missing"); code == 0 {
		t.Fatalf("link outside home must be refused")
	}
}

func TestBundleRoundTripAndImport(t *testing.T) {
	dir := setup(t, "UUID=r / ext4 defaults 0 1\nUUID=gone /mnt/old ext4 defaults,nofail 0 2\n")
	b, err := buildBundle(false, 1)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	bf := filepath.Join(dir, "b.json")
	os.WriteFile(bf, raw, 0o644)
	os.WriteFile(fstabPath(), []byte("UUID=new / ext4 defaults 0 1\n"), 0o644)
	code, out := op(t, true, "import", bf, "--no-such")
	if code == 0 {
		t.Fatalf("unknown option must fail: %s", out)
	}
	code, out = op(t, true, "import", bf)
	if code != 0 || !strings.Contains(out, "add-disabled") {
		t.Fatalf("import failed: %d %s", code, out)
	}
	txt, _ := os.ReadFile(fstabPath())
	if !strings.Contains(string(txt), disabledMark) || strings.Count(string(txt), "UUID=new") != 1 {
		t.Fatalf("unexpected fstab after import:\n%s", txt)
	}
}

func TestDiffDevices(t *testing.T) {
	a := []Device{{Path: "/dev/sda1", UUID: "u1", Fstype: "ext4"}, {Path: "/dev/sdb1", UUID: "u2", Fstype: "exfat"}}
	b := []Device{{Path: "/dev/sda1", UUID: "u1", Fstype: "ext4"}, {Path: "/dev/sdc1", UUID: "u3", Fstype: "vfat"}}
	added, removed := diffDevices(a, b)
	if len(added) != 1 || added[0].Path != "/dev/sdc1" || len(removed) != 1 || removed[0].Path != "/dev/sdb1" {
		t.Fatalf("bad diff: %+v %+v", added, removed)
	}
	// reformatting in place is an addition, not a removal
	c := []Device{{Path: "/dev/sda1", UUID: "NEW", Fstype: "ext4"}}
	added, removed = diffDevices(a[:1], c)
	if len(added) != 1 || len(removed) != 0 {
		t.Fatalf("reformat should be 'added' only: %+v %+v", added, removed)
	}
}

func TestSpecMatches(t *testing.T) {
	d := Device{Path: "/dev/sdb1", UUID: "ABCD-1234", Label: "USB", PartUUID: "pp-1"}
	for spec, want := range map[string]bool{"UUID=abcd-1234": true, "LABEL=USB": true, "LABEL=other": false,
		"PARTUUID=PP-1": true, "/dev/sdb1": true, "/dev/sdc1": false, "UUID=nope": false} {
		if specMatches(spec, d) != want {
			t.Errorf("specMatches(%q) = %v, want %v", spec, !want, want)
		}
	}
}

func TestWatchDetectsInsertion(t *testing.T) {
	dir := setup(t, "UUID=known /mnt/known ext4 defaults,nofail 0 2\n")
	_ = dir
	var mu sync.Mutex
	sigv, devs := "s0", []Device{{Path: "/dev/sda1", UUID: "r", Fstype: "ext4", Type: "part"}}
	sig := func() string { mu.Lock(); defer mu.Unlock(); return sigv }
	list := func() []Device { mu.Lock(); defer mu.Unlock(); return append([]Device(nil), devs...) }
	h := newHub()
	ch := h.subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watch(ctx, h, 10*time.Millisecond, 20*time.Millisecond, sig, list, nil)
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	devs = append(devs, Device{Path: "/dev/sdb1", UUID: "new", Fstype: "exfat", Type: "part", Size: "64G"},
		Device{Path: "/dev/sdc1", UUID: "known", Fstype: "ext4", Type: "part"})
	sigv = "s1"
	mu.Unlock()
	select {
	case ev := <-ch:
		if ev.Type != "devices" || len(ev.Added) != 2 {
			t.Fatalf("unexpected event: %+v", ev)
		}
		for _, d := range ev.Added {
			if d.Path == "/dev/sdb1" && (d.InFstab || !d.Candidate) {
				t.Fatalf("new disk misclassified: %+v", d)
			}
			if d.Path == "/dev/sdc1" && !d.InFstab {
				t.Fatalf("known disk should be flagged in_fstab: %+v", d)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event after insertion")
	}
	// removal
	mu.Lock()
	devs, sigv = devs[:1], "s2"
	mu.Unlock()
	select {
	case ev := <-ch:
		if len(ev.Removed) != 2 {
			t.Fatalf("expected 2 removed, got %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event after removal")
	}
	// mount-only change gives a refresh
	mu.Lock()
	sigv = "s3"
	mu.Unlock()
	select {
	case ev := <-ch:
		if ev.Type != "refresh" {
			t.Fatalf("expected refresh, got %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no refresh event")
	}
}

func TestQuotedUUIDParses(t *testing.T) {
	e, err := parseLine(`UUID="1234-ABCD" /mnt/usb vfat defaults,nofail 0 2`)
	if err != nil || e == nil {
		t.Fatalf("quoted UUID line rejected: %v", err)
	}
	if e.Spec != "UUID=1234-ABCD" {
		t.Fatalf("spec not unquoted: %q", e.Spec)
	}
}
