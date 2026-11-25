package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runSwap(t *testing.T, apply bool, args ...string) (int, string) {
	t.Helper()
	var b bytes.Buffer
	code := RunOp(append([]string{"swap"}, args...), apply, &b)
	return code, b.String()
}

func swapOn(name string) bool {
	for _, a := range readProcSwaps() {
		if realpath(a.name) == realpath(name) {
			return true
		}
	}
	return false
}

// canSwap skips unless this machine lets the test turn swap on and off.
func canSwap(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	for _, c := range []string{"mkswap", "swapon", "swapoff", "fallocate"} {
		if !have(c) {
			t.Skip(c + " not installed")
		}
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "probe")
	exec.Command("fallocate", "-l", "64M", p).Run()
	os.Chmod(p, 0o600)
	exec.Command("mkswap", p).Run()
	if exec.Command("swapon", p).Run() != nil {
		t.Skip("swapon not allowed here")
	}
	exec.Command("swapoff", p).Run()
}

func allowTmp(t *testing.T) {
	old := swapForbidden
	swapForbidden = nil
	t.Cleanup(func() { swapForbidden = old })
}

func TestParseSwapSize(t *testing.T) {
	for in, want := range map[string]int64{"4G": 4 * GiB, "512M": 512 * MiB, "1.5GiB": 1536 * MiB, "2gb": 2 * GiB, "1T": 1024 * GiB} {
		if got, err := parseSwapSize(in); err != nil || got != want {
			t.Errorf("%s: got %d %v, want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"4", "lots", "64M", "-1G"} {
		if _, err := parseSwapSize(bad); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
	if human(4*GiB) != "4 GiB" || human(1536*MiB) != "1.5 GiB" || human(512*MiB) != "512 MiB" {
		t.Error("human sizes")
	}
}

func TestSwapStateFromProcAndFstab(t *testing.T) {
	dir := setup(t, "")
	sw := filepath.Join(dir, "swapfile")
	os.WriteFile(sw, make([]byte, 4096), 0o600)
	fstab := sw + " none swap sw 0 0\n/dev/zram0 none swap sw 0 0\nUUID=dead-beef none swap sw 0 0\n"
	os.WriteFile(os.Getenv("FSTABCTL_FSTAB"), []byte(fstab), 0o644)
	ps := filepath.Join(dir, "swaps")
	os.WriteFile(ps, []byte("Filename\tType\tSize\tUsed\tPriority\n"+sw+" file 4194300 1024 -2\n/dev/manual partition 1000 0 -3\n"), 0o644)
	mi := filepath.Join(dir, "meminfo")
	os.WriteFile(mi, []byte("MemTotal:       16000000 kB\nMemAvailable:    8000000 kB\n"), 0o644)
	oldS, oldM := procSwaps, procMeminfo
	procSwaps, procMeminfo = ps, mi
	defer func() { procSwaps, procMeminfo = oldS, oldM }()

	f, _ := loadFstab()
	st := swapState(f.Entries())
	if st.MemTotal != 16000000*1024 || st.MemAvailable != 8000000*1024 {
		t.Fatalf("meminfo: %+v", st)
	}
	if len(st.Items) != 4 {
		t.Fatalf("want 4 items, got %+v", st.Items)
	}
	a := st.Items[0]
	if a.Kind != "file" || !a.Active || a.Entry != 1 || a.Used != 1024*1024 || a.Priority != -2 {
		t.Errorf("swap file: %+v", a)
	}
	if st.Items[1].Kind != "zram" {
		t.Errorf("zram: %+v", st.Items[1])
	}
	if !st.Items[2].Missing || st.Items[2].Kind != "partition" {
		t.Errorf("missing partition: %+v", st.Items[2])
	}
	if m := st.Items[3]; m.Entry != 0 || !m.Active || m.Name != "/dev/manual" {
		t.Errorf("active, not in fstab: %+v", m)
	}
	// turning off needs room in memory
	big := SwapItem{Name: "x", Active: true, Used: 9000000 * 1024}
	if canSwapOff(big) == nil {
		t.Error("should refuse swapoff with too little free memory")
	}
	if canSwapOff(SwapItem{Name: "x", Active: true, Used: 1024}) != nil {
		t.Error("small swap should turn off")
	}
}

func TestSwapCreateResizeRemove(t *testing.T) {
	canSwap(t)
	allowTmp(t)
	dir := setup(t, "")
	fst := os.Getenv("FSTABCTL_FSTAB")
	sw := filepath.Join(dir, "swapfile")
	t.Cleanup(func() { exec.Command("swapoff", sw).Run() })

	// preview changes nothing
	code, out := runSwap(t, false, "create", sw, "--size", "256M")
	if code != 0 || !strings.Contains(out, "Will create a 256 MiB swap file") || !strings.Contains(out, "dry run") {
		t.Fatalf("preview: %d\n%s", code, out)
	}
	if lexists(sw) {
		t.Fatal("preview made the file")
	}
	// create
	code, out = runSwap(t, true, "create", sw, "--size", "256M", "--priority", "5")
	if code != 0 {
		t.Fatalf("create: %d\n%s", code, out)
	}
	if strings.Contains(out, "mount -a") {
		t.Error("swap output shouldn't suggest mount -a")
	}
	fi, err := os.Stat(sw)
	if err != nil || fi.Size() != 256*MiB || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", fi, err)
	}
	b, _ := os.ReadFile(fst)
	if !strings.Contains(string(b), sw) || !strings.Contains(string(b), "sw,pri=5") {
		t.Fatalf("fstab: %s", b)
	}
	if !swapOn(sw) {
		t.Fatal("not turned on")
	}
	uuid, ok := swapHeader(sw)
	if !ok || uuid == "" {
		t.Fatal("no swap signature")
	}
	// creating again is refused
	if code, _ := runSwap(t, true, "create", sw, "--size", "256M"); code == 0 {
		t.Fatal("should refuse an existing file")
	}
	// resize by entry number, guarded
	if code, out := runSwap(t, true, "resize", "1", "--size", "384M", "--expect", "/somewhere/else"); code == 0 {
		t.Fatalf("stale --expect should be refused\n%s", out)
	}
	code, out = runSwap(t, true, "resize", "1", "--size", "384M", "--expect", sw)
	if code != 0 {
		t.Fatalf("resize: %d\n%s", code, out)
	}
	if fi, _ := os.Stat(sw); fi.Size() != 384*MiB {
		t.Fatalf("size after resize: %d", fi.Size())
	}
	if u2, _ := swapHeader(sw); u2 != uuid {
		t.Errorf("UUID changed: %s -> %s", uuid, u2)
	}
	if !swapOn(sw) {
		t.Fatal("not back on after resize")
	}
	// off / on
	if code, out := runSwap(t, true, "off", sw); code != 0 || swapOn(sw) {
		t.Fatalf("off: %d %s", code, out)
	}
	if code, out := runSwap(t, true, "on", sw); code != 0 || !swapOn(sw) {
		t.Fatalf("on: %d %s", code, out)
	}
	// remove
	code, out = runSwap(t, true, "remove", sw)
	if code != 0 {
		t.Fatalf("remove: %d\n%s", code, out)
	}
	if lexists(sw) || swapOn(sw) {
		t.Fatal("file still there or on")
	}
	if b, _ := os.ReadFile(fst); strings.Contains(string(b), sw) {
		t.Fatalf("still in fstab: %s", b)
	}
}

func TestSwapRefusals(t *testing.T) {
	dir := setup(t, "")
	fst := os.Getenv("FSTABCTL_FSTAB")
	// not a swap file: never deleted
	notSwap := filepath.Join(dir, "important")
	os.WriteFile(notSwap, []byte("my data"), 0o600)
	os.WriteFile(fst, []byte(notSwap+" none swap sw 0 0\n/dev/sda9 none swap sw 0 0\n/dev/zram0 none swap sw 0 0\n"), 0o644)
	if code, out := runSwap(t, true, "remove", notSwap); code == 0 || !strings.Contains(out, "doesn't look like a swap file") {
		t.Fatalf("remove non-swap: %d %s", code, out)
	}
	if !lexists(notSwap) {
		t.Fatal("deleted a file that isn't swap")
	}
	if code, out := runSwap(t, true, "resize", notSwap, "--size", "1G"); code == 0 || !strings.Contains(out, "isn't a swap file") {
		t.Fatalf("resize non-swap: %d %s", code, out)
	}
	// partitions can't be resized; zram is the system's
	if code, out := runSwap(t, false, "resize", "2", "--size", "1G"); code == 0 || !strings.Contains(out, "missing") && !strings.Contains(out, "doesn't exist") && !strings.Contains(out, "partition") {
		t.Fatalf("resize partition: %d %s", code, out)
	}
	if code, out := runSwap(t, false, "remove", "3"); code == 0 || !strings.Contains(out, "zram") {
		t.Fatalf("remove zram: %d %s", code, out)
	}
	// forbidden places and filesystems
	for _, p := range []string{"/etc/swapfile", "/proc/x", "relative"} {
		if code, _ := runSwap(t, false, "create", p, "--size", "1G"); code == 0 {
			t.Errorf("%s should be refused", p)
		}
	}
	if _, err := checkSwapPath("/swapfile-test", 1<<50, 0); err == nil {
		t.Error("should refuse a size bigger than the drive")
	}
	if code, out := runSwap(t, false, "nonsense"); code == 0 || !strings.Contains(out, "unknown swap action") {
		t.Errorf("bad action: %s", out)
	}
}

func TestSwapSetupPartition(t *testing.T) {
	canSwap(t)
	if !have("losetup") || !have("mkfs.ext4") {
		t.Skip("needs losetup and mkfs.ext4")
	}
	dir := setup(t, "")
	img := filepath.Join(dir, "disk.img")
	exec.Command("fallocate", "-l", "200M", img).Run()
	exec.Command("mkfs.ext4", "-q", "-L", "OLDDATA", img).Run()
	o, err := exec.Command("losetup", "--show", "-f", img).Output()
	if err != nil {
		t.Skip("losetup not allowed here")
	}
	loop := strings.TrimSpace(string(o))
	t.Cleanup(func() { exec.Command("swapoff", loop).Run(); exec.Command("losetup", "-d", loop).Run() })
	js := filepath.Join(dir, "lsblk.json")
	writeLs := func(fstype string) {
		os.WriteFile(js, []byte(fmt.Sprintf(`{"blockdevices":[{"name":"%s","path":"%s","type":"part","size":"200M","fstype":%q,"label":"OLDDATA","uuid":"u","mountpoints":[null]}]}`,
			filepath.Base(loop), loop, fstype)), 0o644)
	}
	writeLs("ext4")
	t.Setenv("FSTABCTL_LSBLK_JSON", js)
	if code, out := runSwap(t, true, "setup", loop); code == 0 || !strings.Contains(out, "--erase") {
		t.Fatalf("must ask for --erase: %d %s", code, out)
	}
	code, out := runSwap(t, false, "setup", loop, "--erase")
	if code != 0 || !strings.Contains(out, "ERASE") {
		t.Fatalf("preview: %d %s", code, out)
	}
	code, out = runSwap(t, true, "setup", loop, "--erase")
	if code != 0 {
		t.Fatalf("setup: %d\n%s", code, out)
	}
	uuid, ok := swapHeader(loop)
	if !ok || uuid == "" {
		t.Fatal("not swap")
	}
	b, _ := os.ReadFile(os.Getenv("FSTABCTL_FSTAB"))
	if !strings.Contains(string(b), "UUID="+uuid) {
		t.Fatalf("fstab: %s", b)
	}
	if !swapOn(loop) {
		t.Fatal("not on")
	}
	writeLs("swap")
	if code, out := runSwap(t, true, "setup", loop); code == 0 || !strings.Contains(out, "already") {
		t.Fatalf("second setup: %d %s", code, out)
	}
}
