# Development notes

## Layout

```
cli/fstabctl            Python CLI (single file, standard library only)
gui/                    Go desktop app, fstab Manager (module "fstab-manager", uses Wails v2)
  main.go               flags, window (Wails options), hidden "exec" helper
  app.go                methods the page calls directly: save/open dialogs, show in folder; disk events
  server.go             in-process API handler, state, privilege handling
  watcher.go            disk-insertion detection + event hub
  ops.go                add/set/remove/enable/disable/backup/diff/restore/import/mount/repair
  analyze.go            findings: explanations and one-click fixes
  links.go              home-folder symlink operations and snapshots
  fstab.go devices.go backup.go report.go diff.go util.go osutil.go
  ui.html               the whole interface (HTML + CSS + JS), embedded with go:embed
  icon.png              window icon
  *_test.go             unit and behaviour tests
packaging/              build.sh, desktop file, polkit policy, man pages, icons/
docs/                   this documentation
```

## Building and testing

```sh
make test         # go vet + go test in gui/ (no GTK needed)
make build        # gui/fstab-manager for this machine
make packages     # .deb + .rpm for this machine's architecture into dist/packages
```

Requirements:

- Go 1.22+ and a C compiler.
- GTK 3 and WebKitGTK 4.1 development files. Debian/Ubuntu: `apt install build-essential libgtk-3-dev libwebkit2gtk-4.1-dev`.
  Fedora: `dnf install gcc gtk3-devel webkit2gtk4.1-devel`.
- `dpkg-deb` and `dpkg-shlibdeps` for `.deb`, `rpmbuild` for `.rpm`, Python 3.8+ for the CLI.

The app is built with the Wails build tags directly, so the `wails` CLI tool is not needed:

```sh
cd gui && go build -tags desktop,production,webkit2_41 -o fstab-manager .
```

Without those tags the program compiles (tests use this) but the window won't open.

Because the app links against WebKitGTK, packages are built **natively**: run `packaging/build.sh` on an arm64 machine
to get arm64 packages. `build.sh` options (environment): `MAINTAINER="Name <mail>"`, `LICENSE_ID=MIT`, `GUI_REV`, `CLI_REV`.
Bump the version in `gui/util.go` (`const version`) or `cli/fstabctl` (`VERSION =`) and the package version follows. Reset
the revision to 1 for a new upstream version and raise it when repackaging the same version. The `.deb` dependencies are
worked out by `dpkg-shlibdeps`; the `t64` library names of Ubuntu 24.04 get the older names as alternatives, so the package
also installs on Ubuntu 22.04. `rpmbuild` adds library requirements automatically. The packages pass `lintian`.

### Go modules behind a restricted network

`go.mod` contains `replace` lines that point `golang.org/x/...` at their official GitHub mirrors, so the build works where
only GitHub is reachable. If your network reaches `proxy.golang.org`, they are harmless.

### Test hooks

| Hook | Effect |
|---|---|
| `FSTABCTL_FSTAB`, `FSTABCTL_BACKUP_DIR`, `FSTABCTL_HOME` | operate on a scratch fstab, backup folder and home |
| `FSTABCTL_LSBLK_JSON=file` | the app reads `lsblk -J` output from a file; edit it to simulate plugging a disk in or out |

Run the app against scratch data, never `/etc/fstab`, while developing:

```sh
export FSTABCTL_FSTAB=/tmp/t/fstab FSTABCTL_BACKUP_DIR=/tmp/t/bk FSTABCTL_HOME=/tmp/t/home
cd gui && go run -tags desktop,production,webkit2_41 . --no-notify
```

For automated checks without a desktop, run it on a virtual display (`Xvfb :92 & DISPLAY=:92 …`) inside
`dbus-run-session`, and drive it with `xdotool`.

## Architecture of the desktop app

The window is created by Wails. The page (`ui.html`) is served by an `http.Handler` that Wails calls **in-process** for
the page's requests (there is no listening socket). Wails adds its runtime to the page, which gives it
`window.runtime` (events, quit) and `window.go.main.App` (the bound methods in `app.go`).

| Path or method | Purpose |
|---|---|
| `GET /` | the page |
| `GET /icon.png` | the header icon |
| `GET /api/state?depth=&hidden=` | everything the page renders: entries, devices, findings (with fixes), links, backups, presets |
| `POST /api/run` | `{ "args": [...], "apply": bool }` → `{ "code": n, "output": "…" }` |
| `App.SaveExport(kind)` | native Save dialog (opens in Downloads) for `report-md`, `report-txt` or `bundle`; returns the path or "" |
| `App.ShowInFolder(path)` | opens the folder of a file saved in this session |
| `App.ChooseBundle()` | native Open dialog; checks the file and copies it to the private folder; returns `{path, name}` |
| event `devices` | sent to the page when disks are added or removed (`refresh` for other changes) |

**The page talks in CLI verbs.** `/api/run` takes the same arguments as the command line (`["add","/dev/sdb1","/mnt/data",
"-p","ssd"]`). A preview runs with `apply:false`; **Apply** reruns with `apply:true`. This keeps one code path for previews and
changes and makes every action reproducible by hand. Output text is shown as-is, with diff lines coloured.

**Findings carry their fixes.** `analyze()` and `validateFindings()` return, for each problem, `why`, `fix` and `actions`
(label + CLI arguments). Actions that target an entry use its `#` number plus `--expect SPEC`; repairs use the line number
plus `--expect` with the line's text. The page shows each action through the same preview/Apply dialog.

**Privilege.** Previews and non-fstab operations run in-process as the user. An applying call for a privileged verb,
when the process lacks write access, re-executes `fstab-manager exec -y --real-user … --backup-dir … -- <args>` via `pkexec`
or `sudo -n`. Settings travel as arguments because `pkexec` clears the environment.

**Disk watcher.** Every two seconds a cheap fingerprint is taken (`/proc/partitions`, `/proc/self/mounts`, the
`/dev/disk/by-uuid` listing). When it changes and stays stable for 800 ms, `lsblk` is run, the device list is diffed
by path + UUID + filesystem, and a `devices` or `refresh` event is emitted to the window. Devices are matched to fstab by
identity (UUID/LABEL/PARTUUID/path), because symlinks lag right after plug-in or removal.

## Compatibility rules

The CLI and the app are independent implementations that share data. Keep these stable:

1. **Backup naming and metadata** (`fstab-<ID>.bak/.json`, `links-<ID>.json`) and the same-second `-N` suffix ordering.
2. **Bundle schema** ([REFERENCE.md](REFERENCE.md#export-bundle)). Add fields freely; never rename or remove existing
   ones; keep `"tool": "fstabctl"`.
3. **The disabled marker** `#fstabctl-disabled#` and the aligned line format.
4. **Verbs and flags** used by the page: removing or renaming a CLI flag that `ui.html` or `analyze.go` sends breaks the app.
5. **The polkit action** is bound to `/usr/bin/fstab-manager`; moving the binary needs a matching policy change.

When changing behaviour, change it in both programs, update the tests, and update [REFERENCE.md](REFERENCE.md) and
[CHANGELOG.md](../CHANGELOG.md).

## Adding a command

1. Implement it in `cli/fstabctl` (argparse subparser + `cmd_…`).
2. Implement it in `gui/ops.go` (`RunOp` switch + a `cmd…` method) if the page needs it, and add it to the allow-lists in
   `gui/server.go` (`allowedCmds` / `privCmds`).
3. Add UI in `gui/ui.html` that builds the argument list, or a fix action in `gui/analyze.go`.
4. Add a test in `gui/*_test.go`; run `make test`.
5. Document it in `docs/` and the man pages.

## Roadmap ideas

- `/etc/crypttab` support for encrypted drives; a "mount now" button on the new-disk banner.
- An Arch Linux `PKGBUILD`, a Flatpak, and an arm64 build on native hardware.
- More languages for the interface.
