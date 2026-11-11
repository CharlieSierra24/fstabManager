# Reference

Exact behaviour of every command, option, environment variable, exit code and file format.
For explanations and workflows see [USER-GUIDE.md](USER-GUIDE.md).

## Contents

1. [Command line: `fstabctl`](#command-line-fstabctl)
2. [Desktop app: `fstab-manager`](#desktop-app-fstab-manager)
3. [Environment variables](#environment-variables)
4. [Exit codes](#exit-codes)
5. [Files and locations](#files-and-locations)
6. [File formats](#file-formats)
7. [Import rules](#import-rules)
8. [Validation and findings](#validation-and-findings)

## Command line: `fstabctl`

```
fstabctl [--version] COMMAND [options]
```

Options accepted by every command that changes something: `-n`/`--dry-run` (show the diff or plan, write nothing),
`-y`/`--yes` (do not ask for confirmation), `--fstab PATH` (operate on another file instead of `/etc/fstab`).
Without `-y`, a change asks `Apply this change? [y/N]`; when input is not a terminal it declines and tells you to use `-y`.

### Reading

| Command | Purpose |
|---|---|
| `list` | fstab entries with live status |
| `scan` | disks and partitions, with an "in fstab" column; lists formatted devices not yet configured |
| `verify` | validation errors and findings, plus `findmnt --verify` output. Exit 1 if there are errors |
| `report [-f text\|markdown] [-o FILE]` | human-readable report (format is Markdown if `-o` ends in `.md`) |
| `presets` | the option presets |
| `backups` | list fstab backups |
| `diff [BACKUP]` | current fstab against a backup (default `latest`) |

### Changing fstab

| Command | Options |
|---|---|
| `add [DEVICE [MOUNTPOINT]]` | `-t/--fstype`, `-o/--options` (overrides preset), `-p/--preset {data,ssd,usb,windows,nfs,cifs}`, `--by {uuid,label,partuuid,path}` (default `uuid`), `--dump N`, `--passno N` (default 2 for ext2/3/4, otherwise 0), `--no-mkdir` |
| `set TARGET` | `-o/--options` (replace all), `--add-options a,b`, `--remove-options a,b`, `-t/--fstype`, `--device`, `--mountpoint`, `--passno`, `--expect SPEC` |
| `remove TARGET` | `--force` (needed for `/`, `/boot`, `/boot/efi`, `/efi`), `--expect SPEC` |
| `disable TARGET` / `enable TARGET` | `--force` (to disable a boot-critical mount), `--expect SPEC` |
| `repair` | `--comment-out LINE` or `--add-defaults LINE` (one is required), `--expect TEXT`. See [Repairs](#repairs) |
| `restore [BACKUP]` | backup ID, unique ID prefix, `latest`, or a file path; the current file is backed up first |
| `import BUNDLE` | `--include-system`, `--skip-missing` (see [Import rules](#import-rules)) |
| `backup` | `--note TEXT`; always creates a backup, even if identical to the last |

`DEVICE` may be `/dev/sdb1`, a device name, `UUID=…`, `LABEL=…`, `PARTUUID=…`, `host:/path` (NFS) or `//host/share`
(SMB). Network shares require `-t`. For `UUID=…` style specs without `-t`, the filesystem is detected from the disk or the
command stops and asks for `-t`. With no `DEVICE`, `add` runs an interactive wizard (it needs a terminal).
`TARGET` is a mount point, a device spec, or the `#` number shown by `list`. `--expect SPEC` makes the command act only
if that entry's device is still `SPEC`, which guards a `#` number against an fstab that changed in between (the app always
sends it).

### Mounting without a reboot

| Command | Options |
|---|---|
| `mount TARGET` | mount one entry now; `--expect SPEC` |
| `mount --all` | mount every enabled entry that should be mounted but isn't |

A plan is printed first (`-n` stops there). `--all` skips entries that are disabled, swap, already mounted, boot-critical,
`noauto`/`x-systemd.automount` (they mount on first access), or whose disk isn't connected; each skip is listed with its
reason. For a single `TARGET`, any of these is an error. Before mounting, `systemctl daemon-reload` runs on systemd
systems so the mount units match fstab; missing mount point folders are created. Each result is reported as
`✓ mounted …` or `✗ …: <mount's message>`; exit status 1 if any failed. Needs root.

### Repairs

`repair` only touches a line that can't be read (wrong number of fields, non-numeric dump/pass), and refuses any other line.

- `--comment-out LINE` puts `# ` in front of it and a note after it. The system ignores the line; the text is kept.
- `--add-defaults LINE` completes a line that has exactly device, mount point and a known filesystem type, appending
  `defaults` (plus `nofail` unless it is a boot-critical mount or swap, plus `_netdev` for nfs/nfs4/cifs) and `0 0`.
- `--expect TEXT` acts only if the line (trimmed) still reads `TEXT`.

`LINE` is the line number in the file, as shown in the app and by `verify`.

### Home links: `fstabctl link ACTION`

All link actions accept `--home DIR` (default: your home, even under `sudo`), and the reading actions also accept
`--depth N` (default 1) and `--hidden`.

| Action | Purpose and options |
|---|---|
| `list` | symlinks in home with status and reason |
| `check` | broken links with reasons; exit 1 if any |
| `add TARGET [NAME]` | `--adopt`, `--force`, `--relative`, `--mkdir-target`, `--allow-missing`, `--replace-empty` |
| `set NAME TARGET` | repoint an existing symlink; `--relative`, `--allow-missing` |
| `remove NAME…` | remove symlinks only; `--restore-folder` puts an empty local folder back in each link's place |
| `backup [--note]` / `backups` | snapshot / list snapshots |
| `restore [ID]` | recreate links from a snapshot; `--force` replaces links that now point elsewhere |
| `import BUNDLE` | recreate links from a bundle; `--force`, `--skip-missing`. Also restores Key folders and tracked links |
| `discover` | search home for links, up to `--max-depth N` (default 6) folders deep; `--hidden` searches hidden folders too |
| `track NAME…` / `untrack NAME…` | keep (or stop keeping) these links in backups and bundles |
| `folders [list]` / `folders add NAME…` / `folders remove NAME…` | show Key folders, or add/remove your own (for example `"Claude Assistance"`) |

Rules: `NAME` is relative to home and must stay inside it. Real files and directories are never replaced or removed.
`--adopt` applies when `NAME` is a real directory or file: it moves it to `TARGET` first. It refuses if `TARGET` already
exists, if the parent of `TARGET` does not exist, if `NAME` is a mount point, or if `TARGET` is under an enabled fstab mount
that is not currently mounted (the same check applies to `--mkdir-target`).

`--replace-empty` lets the link replace `NAME` when it is an **empty** folder (nothing inside, hidden files included). A
folder with anything in it is refused, and the folder is removed with `rmdir`, which fails if something was saved into it
in the meantime, so no file can be lost.

**Which links are kept.** Snapshots and bundles keep the links directly in your home folder, plus **tracked** links (any
depth, hidden or not) listed in the settings file. `discover` shows which links are kept.

**Key folders** are Documents, Downloads, Music, Pictures and Videos, under the names in `~/.config/user-dirs.dirs` (so
translated names work), plus your own. For each, `folders` shows whether it is a link, an empty folder, missing, or holds
files.

**Discover** never follows links, skips other drives mounted inside home, app data and toolchain folders (`.cache`,
`.var`, `snap`, `.local/share/Trash`, browser profiles, `node_modules`, `.git`, virtual environments and similar), and
stops after 20,000 folders or 1,000 links. Links inside hidden folders or pointing at system locations (`/usr`, `/opt`,
`/snap`, `/var/lib`, …) are marked as probably made by an app.

**Restoring into empty folders.** `restore` and `import` replace an empty folder with the link (shown as `replace-empty`),
so a fresh install's empty `~/Documents` is no obstacle. Folders with files are skipped.

### Export and launch

| Command | Purpose |
|---|---|
| `export [-o FILE] [--home] [--depth] [--hidden]` | write the bundle (stdout if no `-o`) |
| `gui [--no-notify]` | open fstab Manager (`fstab-manager`, looked up next to the script, then in `PATH`; `fstabctl-gui` is accepted too) |

## Desktop app: `fstab-manager`

```
fstab-manager [--size WIDTHxHEIGHT] [--no-notify] [--version]
```

| Option | Default | Meaning |
|---|---|---|
| `--size WxH` | `1240x820` | Initial window size (at least 640x480; the window can't be made smaller than 860x560) |
| `--no-notify` | off | No native notification (via `notify-send`) when a disk is plugged in |
| `--version` | | Print the version and exit |

The app opens its own window (Wails on WebKitGTK 4.1). It opens no network port. Only one instance runs; starting it again
brings the existing window forward. `fstabctl-gui` is installed as a link to `fstab-manager` for compatibility.

The app supports these operations (the same verbs as the CLI): `add`, `remove`, `enable`, `disable`, `set`, `mount`,
`repair`, `backup`, `diff`, `restore`, `import`, and `link add|set|remove|backup|restore|import|discover|track|untrack|folders`. The interactive wizard,
`verify`, `scan` and `list` are covered by the Overview, Disks and Mounts pages instead. Reports and bundles are saved with
the system's Save dialog, which opens in your Downloads folder (from `~/.config/user-dirs.dirs`, else `~/Downloads`);
bundles to import are chosen with the system's Open dialog.

A hidden subcommand, `fstab-manager exec`, is the privileged helper the app runs through `pkexec` or `sudo`.
It is not meant to be called by hand.

## Environment variables

| Variable | Used by | Meaning |
|---|---|---|
| `FSTABCTL_FSTAB` | CLI, GUI | fstab file to operate on (default `/etc/fstab`); useful for testing |
| `FSTABCTL_BACKUP_DIR` | CLI, GUI | where backups and snapshots go (default `~/.local/share/fstabctl/backups`) |
| `FSTABCTL_REAL_USER` | CLI, GUI | whose home and ownership to use when running as root; set automatically by the app. `SUDO_USER` is used as a fallback |
| `FSTABCTL_HOME` | CLI, GUI | override the home directory (testing) |
| `FSTABCTL_LSBLK_JSON` | GUI | read `lsblk -J` output from this file instead of running it (testing hook for the disk watcher) |
| `DISPLAY`, `WAYLAND_DISPLAY` | GUI | `pkexec` is only used when one of these is set |

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success (including "nothing to change" and dry runs) |
| 1 | Error (message printed as `error: …`), or `verify` found errors, or `link check` found broken links |
| 2 | A change was refused because it would introduce a validation problem, or a command-line usage error |
| 130 | Interrupted (CLI) |

## Files and locations

| Path | Contents |
|---|---|
| `/etc/fstab` | The managed file. Written atomically (temp file + rename), keeping mode and ownership; `restorecon` is run when available |
| `~/.local/share/fstabctl/backups/fstab-<ID>.bak` | Copy of fstab |
| `~/.local/share/fstabctl/backups/fstab-<ID>.json` | Metadata for that backup |
| `~/.local/share/fstabctl/backups/links-<ID>.json` | Home-link snapshot |
| `~/.config/fstabctl/settings.json` | Your Key folders and tracked links: `{"key_folders": [...], "tracked_links": [...]}` (names relative to home). Shared by the CLI and the app |

`<ID>` is a timestamp `YYYYMMDD-HHMMSS`, with `-2`, `-3`… appended if several are created in the same second.
Automatic backups are skipped when the file is identical to the newest backup. Under `sudo` or the app's privileged helper,
files are created in the invoking user's home and handed back to that user.

## File formats

### fstab disabled marker

```
#fstabctl-disabled# UUID=1234-ABCD  /mnt/usb  exfat  defaults,nofail  0  0
```

A line prefixed with `#fstabctl-disabled#` is ignored by the system and by `mount`, and understood by fstabctl.
Comments and unrelated lines in fstab are preserved exactly. Spaces in paths are written as `\040`.

### Backup metadata (`fstab-<ID>.json`)

```json
{
  "id": "20261105-121536",
  "created": "2026-11-05T12:15:36",
  "host": "workstation",
  "distro": "Ubuntu 24.04.4 LTS",
  "note": "auto: before add /mnt/data",
  "sha256": "…",
  "entries": 4
}
```

### Link snapshot (`links-<ID>.json`)

```json
{
  "id": "20261105-121540",
  "created": "2026-11-05T12:15:40",
  "host": "workstation",
  "home": "/home/cam",
  "note": "manual",
  "sha256": "…",
  "links": [ { "name": "Music", "target": "/mnt/data/Music" } ]
}
```

### Export bundle

```json
{
  "tool": "fstabctl",
  "version": "0.4.0",
  "exported": "2026-11-05T12:20:00",
  "host": "workstation",
  "distro": "Ubuntu 24.04.4 LTS",
  "kernel": "6.8.0",
  "entries": [
    { "spec": "UUID=1234-ABCD", "file": "/mnt/data", "fstype": "ext4",
      "options": "defaults,nofail", "dump": 0, "passno": 2, "enabled": true }
  ],
  "devices": [
    { "path": "/dev/sdb1", "size": "931.5G", "fstype": "ext4", "label": "data",
      "uuid": "1234-ABCD", "partuuid": "…", "model": "WD Blue" }
  ],
  "home": "/home/cam",
  "links": [ { "name": "Music", "target": "/mnt/data/Music" } ],
  "raw_fstab": "…full text of fstab…",
  "key_folders": [ "Claude Assistance" ],
  "tracked_links": [ "Projects/site/assets" ]
}
```

`key_folders` and `tracked_links` were added in 0.6.0; older bundles without them still import.

`"tool": "fstabctl"` is the only field required for a file to be accepted. Bundles from the CLI and the app are
interchangeable. They contain your hostname, disk labels and UUIDs, link names and the full fstab text, and never the
contents of credentials files (only their paths); treat them as private.

### Report

Sections: summary, configured mounts, what each entry does (options explained), findings, home links, block devices,
formatted devices not in fstab, and the raw fstab. Available as plain text or Markdown.

## Import rules

For each entry in the bundle, in order:

1. Mount points `/`, `/boot`, `/boot/efi` and `/efi` are **skipped** unless `--include-system`.
2. If the mount point is already enabled in this system's fstab, it is **skipped** (swap entries are skipped when the same
   device is already configured).
3. If the device exists here, the entry is **added** as is.
4. If the device is missing: when the entry's UUID (or label) maps to a label in the bundle's inventory and exactly one
   disk here has that label, the entry is **added and re-linked** to that disk's UUID. Otherwise it is **added as disabled**,
   or skipped when `--skip-missing` is given.
5. Mount point directories are created for enabled entries.

The new fstab is validated and previewed like any other change. Pre-existing problems in your fstab never block an
import; only problems the import itself would introduce do.

Link import: names are resolved inside the current home; targets that began with the bundle's home are rewritten to
the current home; links that already match are skipped; a different existing link needs `--force`; a real file or
directory in the way is always skipped; links whose target is missing are created (and flagged) unless `--skip-missing`.

## Validation and findings

**Errors** (block a change that would introduce them): lines that are not 4–6 fields; non-integer dump/pass; a mount point
that is not an absolute path (other than `none`/`swap`); duplicate enabled mount points.

Values may be quoted, as `blkid` prints them: `UUID="1234-ABCD"` is read as `UUID=1234-ABCD`.

**Findings** (advice, never blocking). In the app each one says why it matters and what to do, and most have buttons
that open the usual preview:

| Finding | Level | Fix buttons |
|---|---|---|
| A line can't be read | problem | Add default options (3-field lines with a known type), Comment out line N |
| Mount point isn't a full path | problem | Disable entry |
| Two entries mount at the same folder | problem | Disable either one |
| The disk for a boot-critical mount can't be found | problem | none: check the UUID before rebooting |
| Disk not connected and no `nofail`/`noauto` | warning | Add nofail, Disable entry |
| Disk not connected (with `nofail`) | note | Disable entry |
| In fstab but not mounted right now | note | Mount now |
| Network share without `_netdev` or automount | warning | Add _netdev |
| Local non-system mount without `nofail` | note | Add nofail |
| `discard` (prefer `fstrim.timer`) | note | Remove discard |
| Credentials file missing or readable by others | warning | none: the fix (`chmod 600`) is shown |
| Kernel name like `/dev/sdb1` instead of `UUID=` | note | Switch to UUID (when the disk is present) |

The same findings, with their explanations, appear in the report. `findmnt --verify` output is shown as advisory text in
previews and by `fstabctl verify`.
