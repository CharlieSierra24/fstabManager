package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed ui.html
var uiHTML string

var (
	allowedCmds     = map[string]bool{"add": true, "remove": true, "enable": true, "disable": true, "set": true, "backup": true, "restore": true, "import": true, "diff": true, "mount": true, "repair": true}
	allowedLinkCmds = map[string]bool{"add": true, "set": true, "remove": true, "backup": true, "restore": true, "import": true,
		"discover": true, "track": true, "untrack": true, "folders": true}
	privCmds = map[string]bool{"add": true, "remove": true, "enable": true, "disable": true, "set": true, "restore": true, "import": true, "mount": true, "repair": true}
)

// privMethod: how this process can write fstab: "root", "direct", "pkexec", "sudo" or "".
func privMethod() string {
	target := fstabPath()
	if !pathExists(target) {
		target = filepath.Dir(target)
	}
	if syscall.Access(target, 2) == nil {
		if os.Geteuid() == 0 {
			return "root"
		}
		return "direct"
	}
	if have("pkexec") && (os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "") {
		return "pkexec"
	}
	if have("sudo") {
		if _, _, code := runCmd("sudo", "-n", "true"); code == 0 {
			return "sudo"
		}
	}
	return ""
}

// ---- state ------------------------------------------------------------

type stateEntry struct {
	N        int         `json:"n"`
	Status   string      `json:"status"`
	Spec     string      `json:"spec"`
	Resolves *string     `json:"resolves"`
	File     string      `json:"file"`
	Fstype   string      `json:"fstype"`
	Options  string      `json:"options"`
	Dump     int         `json:"dump"`
	Passno   int         `json:"passno"`
	Enabled  bool        `json:"enabled"`
	System   bool        `json:"system"`
	Help     [][2]string `json:"help"`
}

type snapView struct {
	ID      string `json:"id"`
	Created string `json:"created"`
	Home    string `json:"home"`
	Note    string `json:"note"`
	Links   int    `json:"links"`
}

type stateInfo struct {
	Host      string  `json:"host"`
	Distro    string  `json:"distro"`
	Kernel    string  `json:"kernel"`
	Fstab     string  `json:"fstab"`
	Home      string  `json:"home"`
	Priv      *string `json:"priv"`
	Version   string  `json:"version"`
	BackupDir string  `json:"backup_dir"`
}

type stateDoc struct {
	Info      stateInfo         `json:"info"`
	Entries   []stateEntry      `json:"entries"`
	Devices   []Device          `json:"devices"`
	Errors    []Finding         `json:"errors"`
	Warnings  []Finding         `json:"warnings"`
	Links     []LinkInfo        `json:"links"`
	KeyFolder []KeyFolder       `json:"key_folders"`
	Backups   []BackupMeta      `json:"backups"`
	LinkSnaps []snapView        `json:"link_snaps"`
	Presets   presetList        `json:"presets"`
	OptHelp   map[string]string `json:"optHelp"`
}

func buildState(depth int, hidden bool) (*stateDoc, error) {
	text, err := readFstab()
	if err != nil {
		return nil, err
	}
	fs := parseFstab(text)
	ents := fs.Entries()
	mounted, swaps := mountedPoints(), swapDevices()
	st := &stateDoc{Entries: []stateEntry{}, Errors: validateFindings(text), Presets: presets, OptHelp: optHelp}
	for i, ie := range ents {
		e := ie.E
		var res *string
		if r := resolve(e.Spec); r != "" {
			res = &r
		}
		help := [][2]string{}
		for _, o := range e.Opts() {
			help = append(help, [2]string{o, explain(o)})
		}
		st.Entries = append(st.Entries, stateEntry{i + 1, statusOf(e, mounted, swaps), e.Spec, res, e.File, e.Fstype,
			e.Options, e.Dump, e.Pass, e.Enabled, systemMounts[e.File], help})
	}
	st.Devices = lsblk()
	if st.Devices == nil {
		st.Devices = []Device{}
	}
	for i := range st.Devices {
		st.Devices[i].InFstab = st.Devices[i].Candidate && inFstab(&st.Devices[i], ents)
	}
	st.Warnings = analyze(ents, st.Devices)
	if st.Warnings == nil {
		st.Warnings = []Finding{}
	}
	home := homeDir()
	// The list shows what the depth/hidden scan finds, plus links you track
	// (which can be deeper or hidden).
	st.Links = currentLinks(home, hidden, depth, ents, mounted)
	have := map[string]bool{}
	for _, l := range st.Links {
		have[l.Name] = true
	}
	for _, l := range managedLinks(home, ents, mounted) {
		if !have[l.Name] {
			st.Links = append(st.Links, l)
		}
	}
	st.KeyFolder = keyFolders(home, ents, mounted)
	st.Backups = listBackups()
	if st.Backups == nil {
		st.Backups = []BackupMeta{}
	}
	st.LinkSnaps = []snapView{}
	for _, s := range listLinkSnaps() {
		st.LinkSnaps = append(st.LinkSnaps, snapView{s.ID, s.Created, s.Home, s.Note, len(s.Links)})
	}
	pm := privMethod()
	var pp *string
	if pm != "" {
		pp = &pm
	}
	st.Info = stateInfo{hostname(), distro(), kernelRelease(), fstabPath(), home, pp, version, backupDir()}
	return st, nil
}

// ---- running operations -------------------------------------------------

type runResult struct {
	Code   int    `json:"code"`
	Output string `json:"output"`
}

func cleanArgs(args []string, tmpdir string) ([]string, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("bad arguments")
	}
	var out []string
	for _, a := range args {
		switch a {
		case "-y", "--yes", "-n", "--dry-run":
			continue
		}
		if strings.HasPrefix(a, "--fstab") || strings.HasPrefix(a, "--home") {
			return nil, fmt.Errorf("option not allowed")
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("bad arguments")
	}
	sub, pos := out[0], 1
	if out[0] == "link" {
		if len(out) < 2 || !allowedLinkCmds[out[1]] {
			return nil, fmt.Errorf("command not allowed")
		}
		sub, pos = out[1], 2
	} else if !allowedCmds[out[0]] {
		return nil, fmt.Errorf("command not allowed")
	}
	if sub == "import" {
		if len(out) <= pos || !strings.HasPrefix(realpath(out[pos]), tmpdir+string(os.PathSeparator)) {
			return nil, fmt.Errorf("choose the bundle with “Choose bundle…” first")
		}
	}
	return out, nil
}

// server answers the window's API requests. There is no network listener:
// Wails hands it the requests made by the app's own page, in-process.
type server struct {
	tmpdir string
	mu     sync.Mutex
	hub    *hub
}

func newServer() (*server, error) {
	tmp, err := os.MkdirTemp("", "fstab-manager-")
	if err != nil {
		return nil, err
	}
	tmp = realpath(tmp)
	os.Chmod(tmp, 0o755) // the privileged helper (root) reads bundles from here
	return &server{tmpdir: tmp, hub: newHub()}, nil
}

func (s *server) execOp(args []string, apply bool) runResult {
	if apply && privCmds[args[0]] {
		switch m := privMethod(); m {
		case "":
			return runResult{126, "No way to get administrator rights from here.\nInstall polkit (pkexec), or use the command line:  sudo fstabctl ..."}
		case "pkexec", "sudo":
			self, err := os.Executable()
			if err != nil {
				return runResult{1, err.Error()}
			}
			argv := []string{self, "exec", "-y", "--real-user", realUser().Username, "--backup-dir", backupDir()}
			if os.Getenv("FSTABCTL_FSTAB") != "" {
				argv = append(argv, "--fstab", fstabPath())
			}
			if h := os.Getenv("FSTABCTL_HOME"); h != "" {
				argv = append(argv, "--home", h)
			}
			argv = append(argv, "--")
			argv = append(argv, args...)
			var cmd *exec.Cmd
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if m == "pkexec" {
				cmd = exec.CommandContext(ctx, "pkexec", argv...)
			} else {
				cmd = exec.CommandContext(ctx, "sudo", append([]string{"-n"}, argv...)...)
			}
			var buf bytes.Buffer
			cmd.Stdout, cmd.Stderr = &buf, &buf
			code := 0
			if err := cmd.Run(); err != nil {
				code = 1
				if ee, ok := err.(*exec.ExitError); ok {
					code = ee.ExitCode()
				} else {
					buf.WriteString(err.Error())
				}
			}
			return runResult{code, nonEmpty(buf.String())}
		}
	}
	var buf bytes.Buffer
	if apply {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	code := RunOp(args, apply, &buf)
	return runResult{code, nonEmpty(buf.String())}
}

func nonEmpty(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return "(no output)"
	}
	return s
}

// ---- HTTP (in-process) -------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	send(w, code, "application/json", b)
}

func send(w http.ResponseWriter, code int, ctype string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'self' 'unsafe-inline'; img-src 'self' data:")
	w.WriteHeader(code)
	w.Write(body)
}

func text(w http.ResponseWriter, code int, msg string) {
	send(w, code, "text/plain; charset=utf-8", []byte(msg))
}

// guard enforces method and content type. Mutating calls must be JSON POSTs,
// which a plain link or form elsewhere can't produce.
func guard(post bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if post {
			if r.Method != http.MethodPost {
				text(w, 405, "POST only")
				return
			}
			if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
				text(w, 415, "json only")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1_000_000)
		} else if r.Method != http.MethodGet {
			text(w, 405, "GET only")
			return
		}
		next(w, r)
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", guard(false, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/index.html":
			send(w, 200, "text/html; charset=utf-8", []byte(uiHTML))
		case "/icon.png":
			send(w, 200, "image/png", iconPNG)
		case "/logo.png":
			send(w, 200, "image/png", logoPNG)
		default:
			text(w, 404, "not found")
		}
	}))
	mux.HandleFunc("/api/state", guard(false, func(w http.ResponseWriter, r *http.Request) {
		depth, _ := strconv.Atoi(r.URL.Query().Get("depth"))
		depth = max(1, min(5, depth))
		st, err := buildState(depth, r.URL.Query().Get("hidden") == "1")
		if err != nil {
			text(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, st)
	}))
	mux.HandleFunc("/api/discover", guard(false, func(w http.ResponseWriter, r *http.Request) {
		found, trunc := discoverLinks(homeDir(), r.URL.Query().Get("hidden") == "1", 6, currentEntriesQuiet(), mountedPoints())
		if found == nil {
			found = []DiscoveredLink{}
		}
		writeJSON(w, 200, map[string]any{"links": found, "truncated": trunc})
	}))
	mux.HandleFunc("/api/run", guard(true, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Args  []string `json:"args"`
			Apply bool     `json:"apply"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			text(w, 400, "bad json")
			return
		}
		args, err := cleanArgs(body.Args, s.tmpdir)
		if err != nil {
			text(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, s.execOp(args, body.Apply))
	}))
	return mux
}
