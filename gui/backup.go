package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BackupMeta matches the fstab-<id>.json sidecar written by the Python CLI.
type BackupMeta struct {
	ID      string `json:"id"`
	Created string `json:"created"`
	Host    string `json:"host"`
	Distro  string `json:"distro"`
	Note    string `json:"note"`
	Sha256  string `json:"sha256"`
	Entries int    `json:"entries"`
	File    string `json:"file,omitempty"`
}

func ensureBackupDir() (string, error) {
	d := backupDir()
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	chownToUser(d, false)
	return d, nil
}

// idKey splits "20261104-123307-2" into ("20261104-123307", 2) so that
// same-second snapshots sort in creation order.
func idKey(path string) (string, int) {
	b := strings.TrimSuffix(filepath.Base(path), ".json")
	if i := strings.Index(b, "-"); i >= 0 {
		b = b[i+1:]
	}
	parts := strings.Split(b, "-")
	if len(parts) >= 3 {
		n, _ := strconv.Atoi(parts[2])
		return parts[0] + "-" + parts[1], n
	}
	return b, 1
}

// globSorted returns matches newest first.
func globSorted(pattern string) []string {
	m, _ := filepath.Glob(pattern)
	sort.Slice(m, func(i, j int) bool {
		ti, ni := idKey(m[i])
		tj, nj := idKey(m[j])
		if ti != tj {
			return ti > tj
		}
		return ni > nj
	})
	return m
}

func listBackups() []BackupMeta {
	var out []BackupMeta
	for _, p := range globSorted(filepath.Join(backupDir(), "fstab-*.json")) {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m BackupMeta
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		m.File = strings.TrimSuffix(p, ".json") + ".bak"
		out = append(out, m)
	}
	return out
}

func uniqueID(dir, prefix, ext string) string {
	base := time.Now().Format("20060102-150405")
	id := base
	for n := 2; pathExists(filepath.Join(dir, prefix+id+ext)); n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}

func makeBackup(note string, dedupe bool) (BackupMeta, error) {
	text, err := readFstab()
	if err != nil {
		return BackupMeta{}, err
	}
	h := sha(text)
	if dedupe {
		if ex := listBackups(); len(ex) > 0 && ex[0].Sha256 == h {
			return ex[0], nil
		}
	}
	d, err := ensureBackupDir()
	if err != nil {
		return BackupMeta{}, err
	}
	id := uniqueID(d, "fstab-", ".bak")
	bak := filepath.Join(d, "fstab-"+id+".bak")
	if err := os.WriteFile(bak, []byte(text), 0o644); err != nil {
		return BackupMeta{}, err
	}
	meta := BackupMeta{ID: id, Created: time.Now().Format("2006-01-02T15:04:05"), Host: hostname(), Distro: distro(),
		Note: note, Sha256: h, Entries: len(parseFstab(text).Entries())}
	mb, _ := json.MarshalIndent(meta, "", "  ")
	mp := filepath.Join(d, "fstab-"+id+".json")
	if err := os.WriteFile(mp, mb, 0o644); err != nil {
		return BackupMeta{}, err
	}
	chownToUser(bak, false)
	chownToUser(mp, false)
	meta.File = bak
	return meta, nil
}

func findBackup(ref string) (BackupMeta, error) {
	bs := listBackups()
	if ref == "" || ref == "latest" {
		if len(bs) == 0 {
			return BackupMeta{}, fmt.Errorf("no backups found in %s", backupDir())
		}
		return bs[0], nil
	}
	for _, b := range bs {
		if b.ID == ref || strings.HasPrefix(b.ID, ref) {
			return b, nil
		}
	}
	return BackupMeta{}, fmt.Errorf("no backup matching '%s'", ref)
}
