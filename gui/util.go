package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const version = "0.6.6"

// invoker is the human who started us, even when we run under sudo/pkexec.
func invoker() string {
	if v := os.Getenv("FSTABCTL_REAL_USER"); v != "" {
		return v
	}
	return os.Getenv("SUDO_USER")
}

func realUser() *user.User {
	if name := invoker(); name != "" {
		if u, err := user.Lookup(name); err == nil {
			return u
		}
	}
	u, err := user.Current()
	if err != nil {
		return &user.User{Username: "root", Uid: "0", Gid: "0", HomeDir: "/root"}
	}
	return u
}

func chownToUser(path string, link bool) {
	if os.Geteuid() != 0 || invoker() == "" {
		return
	}
	u := realUser()
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	if link {
		_ = os.Lchown(path, uid, gid)
	} else {
		_ = os.Chown(path, uid, gid)
	}
}

func backupDir() string {
	if d := os.Getenv("FSTABCTL_BACKUP_DIR"); d != "" {
		return d
	}
	return filepath.Join(realUser().HomeDir, ".local/share/fstabctl/backups")
}

func fstabPath() string {
	if p := os.Getenv("FSTABCTL_FSTAB"); p != "" {
		return p
	}
	return "/etc/fstab"
}

func distro() string {
	b, err := os.ReadFile("/etc/os-release")
	if err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "PRETTY_NAME=") {
				return strings.Trim(strings.TrimPrefix(l, "PRETTY_NAME="), `"' `)
			}
		}
	}
	return "Linux"
}

func kernelRelease() string {
	b, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return strings.TrimSpace(string(b))
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

func sha(text string) string {
	s := sha256.Sum256([]byte(text))
	return hex.EncodeToString(s[:])
}

var octalRe = regexp.MustCompile(`\\([0-7]{3})`)

func unescape(s string) string {
	return octalRe.ReplaceAllStringFunc(s, func(m string) string {
		n, _ := strconv.ParseInt(m[1:], 8, 32)
		return string([]byte{byte(n)})
	})
}

func escape(s string) string {
	r := strings.NewReplacer(`\`, `\134`, " ", `\040`, "\t", `\011`, "\n", `\012`)
	return r.Replace(s)
}

func slug(s string) string {
	re := regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	s = strings.Trim(re.ReplaceAllString(s, "-"), "-")
	if s == "" {
		return "share"
	}
	return s
}

// runCmd runs a command and returns stdout, stderr and exit code (127 if missing).
func runCmd(name string, args ...string) (string, string, int) {
	cmd := exec.Command(name, args...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return o.String(), e.String(), ee.ExitCode()
		}
		return "", err.Error(), 127
	}
	return o.String(), e.String(), 0
}

func have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func lexists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func isSymlink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func realpath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

// table renders rows as aligned text or a Markdown table.
func table(headers []string, rows [][]string, md bool) string {
	if md {
		esc := func(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
		var out []string
		out = append(out, "| "+strings.Join(headers, " | ")+" |")
		seps := make([]string, len(headers))
		for i := range seps {
			seps[i] = "---"
		}
		out = append(out, "|"+strings.Join(seps, "|")+"|")
		for _, r := range rows {
			cells := make([]string, len(r))
			for i, c := range r {
				cells[i] = esc(c)
			}
			out = append(out, "| "+strings.Join(cells, " | ")+" |")
		}
		return strings.Join(out, "\n")
	}
	w := make([]int, len(headers))
	for i, h := range headers {
		w[i] = utf8.RuneCountInString(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if n := utf8.RuneCountInString(c); i < len(w) && n > w[i] {
				w[i] = n
			}
		}
	}
	line := func(cells []string) string {
		parts := make([]string, len(cells))
		for i, c := range cells {
			parts[i] = c + strings.Repeat(" ", w[i]-utf8.RuneCountInString(c))
		}
		return strings.TrimRight(strings.Join(parts, "  "), " ")
	}
	dashes := make([]string, len(headers))
	for i := range dashes {
		dashes[i] = strings.Repeat("-", w[i])
	}
	out := []string{line(headers), line(dashes)}
	for _, r := range rows {
		out = append(out, line(r))
	}
	return strings.Join(out, "\n")
}

func sprintf(f string, a ...any) string { return fmt.Sprintf(f, a...) }
