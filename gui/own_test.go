package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// mountTmpfs mounts a small tmpfs at dir for the test (needs root; skipped otherwise).
func mountTmpfs(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root to mount a test drive")
	}
	os.MkdirAll(dir, 0o755)
	if out, err := exec.Command("mount", "-t", "tmpfs", "-o", "size=4m,mode=0755", "tmpfs", dir).CombinedOutput(); err != nil {
		t.Skipf("can't mount tmpfs here: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("umount", "-l", dir).Run() })
}

func uidOf(t *testing.T, p string) int {
	var st syscall.Stat_t
	if err := syscall.Lstat(p, &st); err != nil {
		t.Fatal(err)
	}
	return int(st.Uid)
}

func TestOwnTopFolderThenEverything(t *testing.T) {
	dir := setup(t, "")
	t.Setenv("FSTABCTL_REAL_USER", "nobody") // the account that should own it
	data := filepath.Join(dir, "data")
	mountTmpfs(t, data)
	os.MkdirAll(filepath.Join(data, "photos"), 0o755)
	os.WriteFile(filepath.Join(data, "photos", "a.jpg"), []byte("x"), 0o644)
	os.Symlink("/etc/hostname", filepath.Join(data, "link")) // must not follow it
	inner := filepath.Join(data, "other")
	mountTmpfs(t, inner) // another drive mounted inside: skipped
	os.WriteFile(filepath.Join(inner, "keep.txt"), []byte("x"), 0o644)
	os.WriteFile(os.Getenv("FSTABCTL_FSTAB"), []byte("tmpfs "+data+" tmpfs defaults 0 0\n"), 0o644)

	if f := ownershipFinding(1, parseFstab(readF(t)).Entries()[0].E, mountedPoints()); f == nil ||
		f.Actions[0].Args[0] != "own" || !strings.Contains(f.Text, "belongs to root") {
		t.Fatalf("expected a take-ownership finding, got %+v", f)
	}
	code, out := op(t, false, "own", data)
	if code != 0 || !strings.Contains(out, "top folder only") || !strings.Contains(out, "dry run") {
		t.Fatalf("preview: %d %s", code, out)
	}
	if code, out := op(t, true, "own", data); code != 0 {
		t.Fatalf("apply: %s", out)
	}
	if uidOf(t, data) != 65534 || uidOf(t, filepath.Join(data, "photos", "a.jpg")) != 0 {
		t.Fatalf("top-only should change just the top folder")
	}
	if f := ownershipFinding(1, parseFstab(readF(t)).Entries()[0].E, mountedPoints()); f != nil {
		t.Fatalf("finding should be gone once you own it: %+v", f)
	}
	code, out = op(t, false, "own", data, "--recursive")
	if code != 0 || !strings.Contains(out, "skipping "+inner) {
		t.Fatalf("recursive preview should skip the inner drive: %s", out)
	}
	if code, out := op(t, true, "own", data, "--recursive"); code != 0 {
		t.Fatalf("recursive apply: %s", out)
	}
	if uidOf(t, filepath.Join(data, "photos", "a.jpg")) != 65534 || uidOf(t, filepath.Join(data, "link")) != 65534 {
		t.Fatalf("recursive should change files and the link itself")
	}
	if uidOf(t, "/etc/hostname") != 0 {
		t.Fatalf("a link's target outside the drive must not change")
	}
	if uidOf(t, filepath.Join(inner, "keep.txt")) != 0 || uidOf(t, inner) != 0 {
		t.Fatalf("another drive mounted inside must not change")
	}
}

func TestOwnRefusals(t *testing.T) {
	dir := setup(t, "")
	t.Setenv("FSTABCTL_REAL_USER", "nobody")
	data := filepath.Join(dir, "data")
	os.MkdirAll(data, 0o755)
	os.WriteFile(os.Getenv("FSTABCTL_FSTAB"), []byte(strings.Join([]string{
		"tmpfs " + data + " tmpfs defaults 0 0",
		"UUID=h /home ext4 defaults 0 2",
		"UUID=w /mnt/win ntfs3 defaults,nofail 0 0",
		"UUID=r / ext4 defaults 0 1",
	}, "\n")+"\n"), 0o644)
	for _, c := range []struct{ target, want string }{
		{data, "isn't mounted"},
		{"/home", "system folder"},
		{"/", "system folder"},
	} {
		if code, out := op(t, true, "own", c.target); code == 0 || !strings.Contains(out, c.want) {
			t.Fatalf("own %s: want refusal %q, got %d %s", c.target, c.want, code, out)
		}
	}
	if !windowsFS["ntfs3"] || ownableFS["ntfs3"] {
		t.Fatal("ntfs3 must use uid=/gid= options, not chown")
	}
	if !allowedCmds["own"] || !privCmds["own"] {
		t.Fatal("own must be allowed from the page and run with administrator rights")
	}
}
