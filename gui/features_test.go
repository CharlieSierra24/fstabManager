package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readF(t *testing.T) string {
	b, err := os.ReadFile(os.Getenv("FSTABCTL_FSTAB"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFindingsExplainAndOfferFixes(t *testing.T) {
	setup(t, "UUID=r / ext4 defaults 0 1\nUUID=gone-1234 /mnt/a ext4 defaults 0 2\n")
	ents := parseFstab(readF(t)).Entries()
	finds := analyze(ents, nil)
	var miss *Finding
	nofailAdvice := 0
	for i := range finds {
		if strings.Contains(finds[i].Text, "isn't connected") {
			miss = &finds[i]
		}
		if strings.Contains(finds[i].Text, "/mnt/a") && strings.Contains(finds[i].Text, "nofail") {
			nofailAdvice++
		}
	}
	if miss == nil || miss.Level != "warn" || miss.Why == "" || miss.Fix == "" {
		t.Fatalf("missing-disk finding not explained: %+v", finds)
	}
	if nofailAdvice != 1 {
		t.Fatalf("nofail advice should appear once for /mnt/a, got %d: %+v", nofailAdvice, finds)
	}
	want := []string{"set", "2", "--expect", "UUID=gone-1234", "--add-options", "nofail"}
	if !reflect.DeepEqual(miss.Actions[0].Args, want) {
		t.Fatalf("fix args = %v, want %v", miss.Actions[0].Args, want)
	}
	if code, out := op(t, true, miss.Actions[0].Args...); code != 0 {
		t.Fatalf("applying the fix failed: %s", out)
	}
	if !strings.Contains(readF(t), "defaults,nofail") {
		t.Fatalf("nofail not added:\n%s", readF(t))
	}
}

func TestStaleEntryGuard(t *testing.T) {
	setup(t, "UUID=a /mnt/a ext4 defaults 0 2\nUUID=b /mnt/b ext4 defaults 0 2\n")
	// The page showed entry 2 as UUID=b, but fstab has since changed.
	os.WriteFile(os.Getenv("FSTABCTL_FSTAB"), []byte("UUID=a /mnt/a ext4 defaults 0 2\nUUID=c /mnt/c ext4 defaults 0 2\n"), 0o644)
	for _, args := range [][]string{
		{"set", "2", "--expect", "UUID=b", "--add-options", "nofail"},
		{"disable", "2", "--expect", "UUID=b"},
		{"remove", "2", "--expect", "UUID=b"},
		{"mount", "2", "--expect", "UUID=b"},
	} {
		code, out := op(t, true, args...)
		if code == 0 || !strings.Contains(out, "changed since this was shown") {
			t.Fatalf("%v should be refused, got %d: %s", args, code, out)
		}
	}
	if strings.Contains(readF(t), "nofail") || strings.Contains(readF(t), disabledMark) {
		t.Fatalf("a stale fix changed the file:\n%s", readF(t))
	}
}

func TestRepairBrokenLines(t *testing.T) {
	setup(t, "UUID=r / ext4 defaults 0 1\nUUID=\"5678-EF\" /mnt/usb vfat\nthis is not valid at all ok\n")
	finds := validateFindings(readF(t))
	if len(finds) != 2 {
		t.Fatalf("want 2 problems, got %+v", finds)
	}
	if finds[0].Actions[0].Label != "Add default options" || finds[0].Why == "" {
		t.Fatalf("3-field line should offer defaults first: %+v", finds[0])
	}
	if code, out := op(t, true, "repair", "--add-defaults", "2", "--expect", "UUID=\"5678-EF\" /mnt/usb vfat"); code != 0 {
		t.Fatalf("add-defaults: %s", out)
	}
	if code, out := op(t, true, finds[1].Actions[0].Args...); code != 0 {
		t.Fatalf("comment-out: %s", out)
	}
	txt := readF(t)
	if len(validateFindings(txt)) != 0 {
		t.Fatalf("problems remain:\n%s", txt)
	}
	if !strings.Contains(txt, "/mnt/usb vfat defaults,nofail 0 0") || !strings.Contains(txt, "# this is not valid at all ok") {
		t.Fatalf("unexpected repair result:\n%s", txt)
	}
	if code, out := op(t, true, "repair", "--comment-out", "1"); code == 0 || !strings.Contains(out, "isn't broken") {
		t.Fatalf("a valid line must not be repaired: %s", out)
	}
	if code, _ := op(t, true, "repair", "--comment-out", "3", "--expect", "something else"); code == 0 {
		t.Fatalf("repair with a stale --expect must be refused")
	}
}

func TestMountPlanning(t *testing.T) {
	dir := setup(t, "")
	x := filepath.Join(dir, "x")
	fstab := strings.Join([]string{
		"tmpfs " + x + " tmpfs defaults 0 0",
		"UUID=gone /mnt/gone ext4 defaults,nofail 0 2",
		"tmpfs " + filepath.Join(dir, "auto") + " tmpfs noauto,x-systemd.automount 0 0",
		disabledMark + " tmpfs " + filepath.Join(dir, "off") + " tmpfs defaults 0 0",
		"/dev/null none swap sw 0 0",
	}, "\n") + "\n"
	os.WriteFile(os.Getenv("FSTABCTL_FSTAB"), []byte(fstab), 0o644)
	code, out := op(t, false, "mount", "--all")
	if code != 0 {
		t.Fatalf("dry run failed: %s", out)
	}
	for _, want := range []string{"Will mount:", x, "disk not connected", "mounts by itself", "disabled", "swap, not a folder", "dry run: nothing mounted"} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan missing %q:\n%s", want, out)
		}
	}
	if code, out := op(t, false, "mount", "/mnt/gone"); code == 0 || !strings.Contains(out, "disk not connected") {
		t.Fatalf("mounting a missing disk should be refused: %s", out)
	}
	if code, _ := op(t, false, "mount"); code == 0 {
		t.Fatalf("mount without a target must fail")
	}
}

func TestMountAndRepairAllowedAndPrivileged(t *testing.T) {
	for _, c := range []string{"mount", "repair"} {
		if !allowedCmds[c] || !privCmds[c] {
			t.Fatalf("%s must be allowed and privileged", c)
		}
	}
	if _, err := cleanArgs([]string{"mount", "--all"}, "/tmp/x"); err != nil {
		t.Fatal(err)
	}
	if _, err := cleanArgs([]string{"mount", "--fstab=/tmp/evil"}, "/tmp/x"); err == nil {
		t.Fatal("--fstab must be refused from the page")
	}
}

func TestStageBundle(t *testing.T) {
	dir := setup(t, "UUID=r / ext4 defaults 0 1\n")
	srv, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(srv.tmpdir)
	bad := filepath.Join(dir, "notes.json")
	os.WriteFile(bad, []byte(`{"hello":1}`), 0o644)
	if _, err := srv.stageBundle(bad); err == nil || !strings.Contains(err.Error(), "not an fstab bundle") {
		t.Fatalf("non-bundle accepted: %v", err)
	}
	content, name, mode, err := exportContent("bundle")
	if err != nil || mode != 0o600 || !strings.HasPrefix(name, "fstab-bundle-") || !strings.HasSuffix(name, ".json") {
		t.Fatalf("bundle export: %v %s %o", err, name, mode)
	}
	good := filepath.Join(dir, name)
	os.WriteFile(good, content, 0o600)
	cb, err := srv.stageBundle(good)
	if err != nil || cb.Name != name || !strings.HasPrefix(cb.Path, srv.tmpdir) {
		t.Fatalf("stage: %+v %v", cb, err)
	}
	if _, err := cleanArgs([]string{"import", cb.Path}, srv.tmpdir); err != nil {
		t.Fatalf("staged bundle should be importable: %v", err)
	}
	if _, err := cleanArgs([]string{"import", good}, srv.tmpdir); err == nil {
		t.Fatalf("a bundle outside the private folder must be refused")
	}
}

func TestReportExplainsFindings(t *testing.T) {
	setup(t, "UUID=gone-1 /mnt/a ext4 defaults 0 2\n")
	content, name, _, err := exportContent("report-md")
	if err != nil || !strings.HasSuffix(name, ".md") {
		t.Fatal(err, name)
	}
	for _, want := range []string{"Why it matters:", "What to do:", "fstab Manager"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("report missing %q", want)
		}
	}
}

func TestDownloadsDirFollowsUserDirs(t *testing.T) {
	dir := setup(t, "")
	home := filepath.Join(dir, "home")
	dl := filepath.Join(home, "Téléchargements")
	os.MkdirAll(dl, 0o755)
	os.MkdirAll(filepath.Join(home, ".config"), 0o755)
	os.WriteFile(filepath.Join(home, ".config", "user-dirs.dirs"), []byte("XDG_DOWNLOAD_DIR=\"$HOME/Téléchargements\"\n"), 0o644)
	if got := downloadsDir(); got != dl {
		t.Fatalf("downloadsDir = %s, want %s", got, dl)
	}
}

func TestPickerStart(t *testing.T) {
	dir := setup(t, "")
	deep := filepath.Join(dir, "a", "b")
	os.MkdirAll(deep, 0o755)
	if got := pickerStart(deep); got != deep {
		t.Fatalf("existing folder: got %s", got)
	}
	if got := pickerStart(filepath.Join(deep, "not", "yet")); got != deep {
		t.Fatalf("missing folder should start at nearest existing parent %s, got %s", deep, got)
	}
	if got := pickerStart(""); got == "" || !isDir(got) {
		t.Fatalf("empty start should give an existing folder, got %q", got)
	}
}

func TestDialogDirFollowsLinks(t *testing.T) {
	dir := setup(t, "")
	real := filepath.Join(dir, "storage", "Downloads")
	os.MkdirAll(real, 0o755)
	link := filepath.Join(dir, "Downloads")
	os.Symlink(real, link)
	if got := dialogDir(link); got != realpath(real) {
		t.Fatalf("linked folder: got %q, want %q", got, realpath(real))
	}
	if fi, err := os.Lstat(dialogDir(link)); err != nil || !fi.IsDir() {
		t.Fatalf("result must pass the dialog's no-follow check")
	}
	os.Symlink(filepath.Join(dir, "gone"), filepath.Join(dir, "Broken"))
	if got := dialogDir(filepath.Join(dir, "Broken")); got != "" {
		t.Fatalf("broken link should give no start folder, got %q", got)
	}
	if got := dialogDir(real); got != realpath(real) {
		t.Fatalf("plain folder: got %q", got)
	}
}
