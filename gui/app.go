package main

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed icon.png
var iconPNG []byte

//go:embed logo.png
var logoPNG []byte

// author and authorURL are shown on the splash screen and in About.
const (
	author    = "csTechnologies"
	authorURL = "https://cstevens.ca"
)

// App holds the methods the window's page can call directly (window.go.main.App.*).
type App struct {
	ctx    context.Context
	srv    *server
	notify bool
	cancel context.CancelFunc

	mu    sync.Mutex
	saved map[string]bool // files this run saved; only these can be shown in the file manager
}

func newApp(srv *server, notify bool) *App {
	return &App{srv: srv, notify: notify, saved: map[string]bool{}}
}

// startup runs once the window exists: start watching for disks and forward
// each change to the page as a "devices" event.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	wctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	ch := a.srv.hub.subscribe()
	go func() {
		defer a.srv.hub.unsubscribe(ch)
		for {
			select {
			case <-wctx.Done():
				return
			case ev := <-ch:
				runtime.EventsEmit(ctx, "devices", ev)
			}
		}
	}()
	var onEvent func(DeviceEvent)
	if a.notify {
		onEvent = notifyDesktop
	}
	go watch(wctx, a.srv.hub, 2*time.Second, 800*time.Millisecond, hwSignature, lsblk, onEvent)
}

func (a *App) shutdown(ctx context.Context) {
	if a.cancel != nil {
		a.cancel()
	}
	os.RemoveAll(a.srv.tmpdir)
}

// exportContent builds a report or bundle and suggests a file name for it.
func exportContent(kind string) (content []byte, name string, mode os.FileMode, err error) {
	stamp := time.Now().Format("2006-01-02")
	host := slug(hostname())
	switch kind {
	case "report-md", "report-txt":
		format, ext := "markdown", "md"
		if kind == "report-txt" {
			format, ext = "text", "txt"
		}
		rep, err := buildReport(format, false, 1)
		if err != nil {
			return nil, "", 0, err
		}
		return []byte(rep), fmt.Sprintf("fstab-report-%s-%s.%s", host, stamp, ext), 0o644, nil
	case "bundle":
		b, err := buildBundle(false, 1)
		if err != nil {
			return nil, "", 0, err
		}
		out, _ := json.MarshalIndent(b, "", "  ")
		// The bundle lists every mount and link; keep it private to you.
		return append(out, '\n'), fmt.Sprintf("fstab-bundle-%s-%s.json", host, stamp), 0o600, nil
	}
	return nil, "", 0, fmt.Errorf("unknown export %q", kind)
}

// SaveExport asks where to save a report or bundle (native dialog, opening in
// Downloads) and writes it there. It returns the saved path, or "" if cancelled.
func (a *App) SaveExport(kind string) (string, error) {
	content, name, mode, err := exportContent(kind)
	if err != nil {
		return "", err
	}
	filter := runtime.FileFilter{DisplayName: "Markdown (*.md)", Pattern: "*.md"}
	switch {
	case strings.HasSuffix(name, ".txt"):
		filter = runtime.FileFilter{DisplayName: "Text (*.txt)", Pattern: "*.txt"}
	case strings.HasSuffix(name, ".json"):
		filter = runtime.FileFilter{DisplayName: "fstab bundle (*.json)", Pattern: "*.json"}
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:                "Save " + map[string]string{"report-md": "report", "report-txt": "report", "bundle": "bundle"}[kind],
		DefaultDirectory:     dialogDir(downloadsDir()),
		DefaultFilename:      name,
		Filters:              []runtime.FileFilter{filter},
		CanCreateDirectories: true,
	})
	if err != nil || path == "" {
		return "", err
	}
	if err := writeFileAs(path, content, mode); err != nil {
		return "", err
	}
	a.mu.Lock()
	a.saved[path] = true
	a.mu.Unlock()
	return path, nil
}

// writeFileAs writes a file and, when running as root for someone, gives it to them.
func writeFileAs(path string, content []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, content, mode); err != nil {
		return err
	}
	os.Chmod(path, mode)
	chownToUser(path, false)
	return nil
}

// ShowInFolder opens the folder holding a file this run saved.
func (a *App) ShowInFolder(path string) error {
	a.mu.Lock()
	ok := a.saved[path]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("not a file saved by fstab Manager")
	}
	if !have("xdg-open") {
		return fmt.Errorf("xdg-open is not installed")
	}
	launch("xdg-open", filepath.Dir(path))
	return nil
}

// ChooseFolder shows the system's folder picker and returns the chosen folder,
// or "" if cancelled. It starts at start (or its parent) when that exists,
// otherwise where drives are usually mounted.
func (a *App) ChooseFolder(title, start string) (string, error) {
	if title == "" {
		title = "Choose a folder"
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:                title,
		DefaultDirectory:     dialogDir(pickerStart(start)),
		CanCreateDirectories: true,
	})
}

func pickerStart(start string) string {
	if start != "" {
		start = filepath.Clean(start)
		for d := start; d != "/" && d != "."; d = filepath.Dir(d) {
			if isDir(d) {
				return d
			}
		}
	}
	user := ""
	if u := realUser(); u != nil {
		user = u.Username
	}
	for _, d := range []string{"/mnt", filepath.Join("/media", user), filepath.Join("/run/media", user), "/media"} {
		if hasSubdirs(d) {
			return d
		}
	}
	return homeDir()
}

func hasSubdirs(dir string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if e.IsDir() {
			return true
		}
	}
	return false
}

// OpenWebsite opens the author's website in the default browser. It only ever
// opens this one fixed address.
func (a *App) OpenWebsite() error {
	if !have("xdg-open") {
		return fmt.Errorf("xdg-open is not installed; visit %s", authorURL)
	}
	launch("xdg-open", authorURL)
	return nil
}

// ChosenBundle is what ChooseBundle returns: the private copy to import from,
// and the original file name to show.
type ChosenBundle struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// ChooseBundle lets the user pick a bundle (native dialog), checks it, and copies
// it to the app's private folder so the privileged helper can read it.
func (a *App) ChooseBundle() (*ChosenBundle, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Choose an fstab bundle",
		DefaultDirectory: dialogDir(downloadsDir()),
		Filters:          []runtime.FileFilter{{DisplayName: "fstab bundle (*.json)", Pattern: "*.json"}},
	})
	if err != nil || path == "" {
		return nil, err
	}
	return a.srv.stageBundle(path)
}

func (s *server) stageBundle(path string) (*ChosenBundle, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	content, err := io.ReadAll(io.LimitReader(f, 6_000_001))
	if err != nil {
		return nil, err
	}
	if len(content) > 6_000_000 {
		return nil, fmt.Errorf("that file is too large to be a bundle")
	}
	var probe struct {
		Tool string `json:"tool"`
	}
	if json.Unmarshal(content, &probe) != nil || probe.Tool != "fstabctl" {
		return nil, fmt.Errorf("%s is not an fstab bundle (export one with “Portable bundle”)", filepath.Base(path))
	}
	tmp, err := os.CreateTemp(s.tmpdir, "bundle-*.json")
	if err != nil {
		return nil, err
	}
	tmp.Write(content)
	tmp.Close()
	os.Chmod(tmp.Name(), 0o644)
	return &ChosenBundle{realpath(tmp.Name()), filepath.Base(path)}, nil
}

// downloadsDir is the user's Downloads folder (XDG user-dirs aware), or home.
func downloadsDir() string {
	home := homeDir()
	if f, err := os.Open(filepath.Join(home, ".config", "user-dirs.dirs")); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			l := strings.TrimSpace(sc.Text())
			if v, ok := strings.CutPrefix(l, "XDG_DOWNLOAD_DIR="); ok {
				v = strings.Trim(v, `"`)
				v = strings.Replace(v, "$HOME", home, 1)
				if isDir(v) {
					return v
				}
			}
		}
	}
	if d := filepath.Join(home, "Downloads"); isDir(d) {
		return d
	}
	return home
}

// launch starts a helper program in the background, as the real user when
// fstab Manager itself was started with sudo.
func launch(args ...string) {
	if os.Geteuid() == 0 && os.Getenv("SUDO_USER") != "" {
		args = append([]string{"sudo", "-u", os.Getenv("SUDO_USER")}, args...)
	}
	cmd := exec.Command(args[0], args[1:]...)
	if cmd.Start() == nil {
		go cmd.Wait()
	}
}

// dialogDir turns a folder into one the native dialogs accept. Wails checks the
// starting folder without following links, so a linked folder such as
// ~/Downloads -> /storage/Downloads is rejected as "does not exist". The real
// folder behind the link is passed instead; if there is none, "" lets the
// dialog open at its own default rather than fail.
func dialogDir(p string) string {
	if p == "" {
		return ""
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return ""
	}
	if fi, err := os.Lstat(real); err != nil || !fi.IsDir() {
		return ""
	}
	return real
}
