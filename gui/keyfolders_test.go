package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func homeSetup(t *testing.T) (home, data string) {
	dir := setup(t, "UUID=r / ext4 defaults 0 1\n")
	home = os.Getenv("FSTABCTL_HOME")
	if home == "" {
		home = filepath.Join(dir, "home")
		t.Setenv("FSTABCTL_HOME", home)
	}
	data = filepath.Join(dir, "data")
	os.MkdirAll(home, 0o755)
	os.MkdirAll(data, 0o755)
	return home, data
}

func TestReplaceEmptyFolderWithLink(t *testing.T) {
	home, data := homeSetup(t)
	os.Mkdir(filepath.Join(home, "Documents"), 0o755)
	target := filepath.Join(data, "Documents")
	code, out := op(t, false, "link", "add", target, "Documents", "--replace-empty", "--mkdir-target")
	if code != 0 || !strings.Contains(out, "replace the empty folder ~/Documents") {
		t.Fatalf("preview: %d %s", code, out)
	}
	if code, out := op(t, true, "link", "add", target, "Documents", "--replace-empty", "--mkdir-target"); code != 0 {
		t.Fatalf("apply: %s", out)
	}
	if got, _ := os.Readlink(filepath.Join(home, "Documents")); got != target || !isDir(target) {
		t.Fatalf("link not made: %q", got)
	}
	// Make local again: link removed, empty folder back.
	if code, out := op(t, true, "link", "remove", "Documents", "--restore-folder"); code != 0 {
		t.Fatalf("restore-folder: %s", out)
	}
	if !isEmptyDir(filepath.Join(home, "Documents")) {
		t.Fatalf("expected an empty local ~/Documents again")
	}
}

func TestNonEmptyFolderIsNeverReplaced(t *testing.T) {
	home, data := homeSetup(t)
	os.MkdirAll(filepath.Join(home, "Pictures"), 0o755)
	os.WriteFile(filepath.Join(home, "Pictures", ".hidden-but-real"), []byte("x"), 0o644)
	code, out := op(t, true, "link", "add", filepath.Join(data, "Pictures"), "Pictures", "--replace-empty", "--mkdir-target")
	if code == 0 || !strings.Contains(out, "isn't empty") {
		t.Fatalf("a folder with files must not be replaced: %d %s", code, out)
	}
	if isSymlink(filepath.Join(home, "Pictures")) || !pathExists(filepath.Join(home, "Pictures", ".hidden-but-real")) {
		t.Fatalf("the folder was touched")
	}
}

func TestKeyFoldersStandardAndCustom(t *testing.T) {
	home, data := homeSetup(t)
	os.MkdirAll(filepath.Join(home, ".config"), 0o755)
	os.WriteFile(filepath.Join(home, ".config", "user-dirs.dirs"), []byte("XDG_MUSIC_DIR=\"$HOME/Musique\"\n"), 0o644)
	os.Mkdir(filepath.Join(home, "Documents"), 0o755)
	os.MkdirAll(filepath.Join(home, "Videos", "clip"), 0o755)
	os.Symlink(filepath.Join(data, "Pics"), filepath.Join(home, "Pictures"))
	if code, out := op(t, true, "link", "folders", "add", "Claude Assistance"); code != 0 {
		t.Fatal(out)
	}
	got := map[string]KeyFolder{}
	var order []string
	for _, k := range keyFolders(home, nil, map[string]bool{}) {
		got[k.Name] = k
		order = append(order, k.Name)
	}
	want := []string{"Documents", "Downloads", "Musique", "Pictures", "Videos", "Claude Assistance"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order %v, want %v", order, want)
	}
	if got["Documents"].State != "empty" || got["Downloads"].State != "missing" || got["Videos"].State != "files" ||
		got["Pictures"].State != "link" || got["Pictures"].OK || !got["Claude Assistance"].Custom {
		t.Fatalf("states: %+v", got)
	}
	if code, _ := op(t, true, "link", "folders", "add", "../escape"); code == 0 {
		t.Fatal("names outside home must be refused")
	}
}

func TestTrackedLinksAreKeptInBackupsAndBundles(t *testing.T) {
	home, data := homeSetup(t)
	os.MkdirAll(filepath.Join(home, "Projects", "site"), 0o755)
	os.Symlink(data, filepath.Join(home, "Projects", "site", "assets"))
	os.Symlink(data, filepath.Join(home, "Data"))
	names := func(ls []LinkInfo) string {
		var n []string
		for _, l := range ls {
			n = append(n, l.Name)
		}
		return strings.Join(n, ",")
	}
	if got := names(managedLinks(home, nil, nil)); got != "Data" {
		t.Fatalf("before tracking: %s", got)
	}
	if code, out := op(t, true, "link", "track", "Projects/site/assets"); code != 0 {
		t.Fatal(out)
	}
	if got := names(managedLinks(home, nil, nil)); got != "Data,Projects/site/assets" {
		t.Fatalf("after tracking: %s", got)
	}
	b, err := buildBundle(false, 1)
	if err != nil || len(b.Links) != 2 || len(b.TrackedLinks) != 1 {
		t.Fatalf("bundle: %+v %v", b, err)
	}
	if code, out := op(t, true, "link", "backup"); code != 0 || !strings.Contains(out, "(2 links)") {
		t.Fatalf("snapshot should keep both: %s", out)
	}
	if code, _ := op(t, true, "link", "track", "Projects"); code == 0 {
		t.Fatal("a real folder can't be tracked as a link")
	}
}

func TestDiscoverSkipsAppDataAndOtherDrives(t *testing.T) {
	home, data := homeSetup(t)
	os.MkdirAll(filepath.Join(home, "Work", "a", "b"), 0o755)
	os.Symlink(data, filepath.Join(home, "Work", "a", "b", "mine"))
	os.MkdirAll(filepath.Join(home, ".cache", "x"), 0o755)
	os.Symlink(data, filepath.Join(home, ".cache", "x", "cachelink"))
	os.MkdirAll(filepath.Join(home, ".config", "app"), 0o755)
	os.Symlink("/usr/share/doc", filepath.Join(home, ".config", "app", "doclink"))
	os.MkdirAll(filepath.Join(home, "code", "node_modules", "p"), 0o755)
	os.Symlink(data, filepath.Join(home, "code", "node_modules", "p", "nm"))
	found, _ := discoverLinks(home, false, 6, nil, nil)
	if len(found) != 1 || found[0].Name != "Work/a/b/mine" || found[0].ByApp || found[0].Kept {
		t.Fatalf("visible search: %+v", found)
	}
	found, _ = discoverLinks(home, true, 6, nil, nil)
	byName := map[string]DiscoveredLink{}
	for _, d := range found {
		byName[d.Name] = d
	}
	if _, ok := byName[".cache/x/cachelink"]; ok {
		t.Fatal(".cache must be skipped")
	}
	if d, ok := byName[".config/app/doclink"]; !ok || !d.ByApp {
		t.Fatalf("hidden app link should be found and flagged: %+v", found)
	}
	if _, ok := byName["code/node_modules/p/nm"]; ok {
		t.Fatal("node_modules must be skipped")
	}
}

func TestImportFillsEmptyFoldersAndRestoresSettings(t *testing.T) {
	home, data := homeSetup(t)
	os.Mkdir(filepath.Join(home, "Documents"), 0o755) // a fresh install's empty folder
	os.MkdirAll(filepath.Join(home, "Music"), 0o755)
	os.WriteFile(filepath.Join(home, "Music", "song.mp3"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(data, "Documents"), 0o755)
	bd := map[string]any{"tool": "fstabctl", "version": "x", "home": "/home/olduser", "entries": []any{}, "devices": []any{},
		"links": []map[string]string{
			{"name": "Documents", "target": filepath.Join(data, "Documents")},
			{"name": "Music", "target": filepath.Join(data, "Music")},
		},
		"key_folders": []string{"Claude Assistance"}, "tracked_links": []string{"Projects/x"}}
	b, _ := json.Marshal(bd)
	p := filepath.Join(home, "..", "bundle.json")
	os.WriteFile(p, b, 0o644)
	code, out := op(t, true, "link", "import", p)
	if code != 0 {
		t.Fatalf("import: %s", out)
	}
	if !isSymlink(filepath.Join(home, "Documents")) {
		t.Fatalf("empty ~/Documents should have been replaced:\n%s", out)
	}
	if isSymlink(filepath.Join(home, "Music")) || !strings.Contains(out, "a real file/directory is in the way") {
		t.Fatalf("~/Music has files and must be skipped:\n%s", out)
	}
	s := loadSettings()
	if strings.Join(s.KeyFolders, ",") != "Claude Assistance" || strings.Join(s.TrackedLinks, ",") != "Projects/x" {
		t.Fatalf("settings not restored: %+v", s)
	}
}

func TestKeyFolderToAnyFolderAndNotInsideItself(t *testing.T) {
	home, data := homeSetup(t)
	storage := filepath.Join(data, "storage") // like /storage/Music: any folder, not a drive
	os.MkdirAll(filepath.Join(storage, "Music"), 0o755)
	os.Mkdir(filepath.Join(home, "Music"), 0o755)
	if code, out := op(t, true, "link", "add", filepath.Join(storage, "Music"), "Music", "--replace-empty"); code != 0 {
		t.Fatalf("link to any folder: %s", out)
	}
	if got, _ := os.Readlink(filepath.Join(home, "Music")); got != filepath.Join(storage, "Music") {
		t.Fatalf("got %q", got)
	}
	os.Mkdir(filepath.Join(home, "Videos"), 0o755)
	code, out := op(t, true, "link", "add", filepath.Join(home, "Videos", "archive"), "Videos", "--replace-empty", "--mkdir-target")
	if code == 0 || !strings.Contains(out, "inside it") || !isEmptyDir(filepath.Join(home, "Videos")) {
		t.Fatalf("a link into itself must be refused and change nothing: %d %s", code, out)
	}
}
