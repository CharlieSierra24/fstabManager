# User guide

This guide walks through the desktop app and the command-line tool, then shows complete workflows.
For every option and file format, see [REFERENCE.md](REFERENCE.md).

## Contents

1. [Concepts](#concepts)
2. [The desktop app](#the-desktop-app)
3. [The command line](#the-command-line)
4. [Workflows](#workflows)
5. [Presets](#presets)

## Concepts

**fstab entry.** One line of `/etc/fstab` saying "mount this device here, as this filesystem, with these options".
fstabctl identifies disks by `UUID=` by default, because names like `/dev/sdb1` can change between boots.

**Preview, then apply.** Every change is computed first and shown as a diff. Nothing touches `/etc/fstab` until you
confirm (the app's **Apply** button, or `-y` on the command line). `-n` always means "preview only".

**Automatic backup.** Right before each write, the current `/etc/fstab` is saved. You can restore any backup later.
Backups live in your home folder (`~/.local/share/fstabctl/backups`), not in `/etc`, so they survive a reinstall
if your home is on a separate disk.

**Disabled entry.** "Disable" does not delete a line. It marks it with `#fstabctl-disabled#` so the system ignores
it but you can re-enable it later. Imported entries whose disk is missing are kept this way.

**Presets.** Ready-made option sets (see [Presets](#presets)). Most include `nofail`, so a missing disk can never stop
your machine booting.

**Bundle.** A JSON file holding your fstab entries, a disk inventory and your home-folder links. It is what you carry
to a new install.

**Home links.** Symlinks inside your home folder, such as `~/Music -> /mnt/data/Music`.

## The desktop app

Start **fstab Manager** from your application menu, or run `fstab-manager` (or `fstabctl gui`). It opens in its own
window, like any other app. Close the window or click **Quit** to exit. Only one copy runs at a time: starting it again
brings the open window forward.

At startup a **splash screen** shows the logo, the version and the author. It closes by itself after 15 seconds (a
bar counts down), or straight away with the **×** in its top-right corner or the Esc key. The same box is the **About**
screen: click the blue **?** in the footer bar at the bottom-left, next to the version number. About stays open until you close it.
Clicking the website (cstevens.ca) opens it in your browser.

The header shows two status chips:

- **● watching disks**: live disk detection is running.
- **changes ask for your password / changes use sudo / full access / read-only**: how fstab changes get
  administrator rights. If it says *read-only*, see [Troubleshooting](TROUBLESHOOTING.md#the-app-says-read-only).

### Overview

Counts (entries, mounted, missing, disabled, broken links), then **Findings**: everything that could cause trouble,
most serious first. Each finding has three parts:

- **What's wrong**, for example "/mnt/archive: the disk (UUID=…) isn't connected, and this entry has no “nofail”".
- **Why it matters**: here, that boot waits about 90 seconds for the disk and then stops in an emergency shell.
- **What to do**, with buttons that do it: **Add nofail…**, **Disable entry…**. Each button opens the usual preview
  with the exact change; nothing happens until you click **Apply**.

Findings include lines that can't be read (with **Add default options…** or **Comment out line N…**), two entries on one
folder, network shares without `_netdev`, `discard`, `/dev/sdX` names (with **Switch to UUID…**), and entries that are
in fstab but not mounted right now (with **Mount now**). The full list is in
[REFERENCE.md](REFERENCE.md#validation-and-findings).

Below that: disks that are formatted but not in fstab, and quick actions: back up now, **Mount all drives now…**, save
the report, save the bundle.

### Mounts

A table of every entry. Option chips show a plain-language explanation when you hover over them.

- **Add mount…** opens the add dialog (below).
- **Edit** changes options, filesystem type or fsck pass. Quick-toggle chips add or remove common options
  (`nofail`, `noatime`, `ro`, `noauto`, `_netdev`, `x-systemd.automount`).
- **Disable / Enable** comments an entry out or brings it back.
- **Remove** deletes the line (the backup keeps it recoverable).
- **Mount now** appears on entries that are in fstab but not mounted. It mounts the drive straight away, so you don't
  have to reboot to use it. **Mount all now…** does this for every such entry (skipping disks that aren't connected and
  drives that mount by themselves when opened).
- `/`, `/boot` and the EFI partition cannot be disabled or removed here. That protection exists to prevent
  an unbootable system; use the CLI with `--force` if you truly need it.

Every action opens a **preview** showing the exact command and the diff (or, for mounting, what will be mounted). Click
**Apply** to commit, or **Cancel**. If it fails, the dialog shows the system's own message (for example from `mount`) and
the reason appears in a red note.

Each action is tied to the line you saw: if fstab changed in the meantime (for example you edited it in a terminal), the
action is refused and you are asked to look again, rather than changing a different line.

### Adding a mount

1. **Mounts → Add mount…**, or click **Add to fstab…** next to a disk.
2. Pick the device, or choose *Network share / other…* and enter `host:/path` (NFS) or `//host/share` (SMB).
3. Check the mount point (suggested from the disk label) and filesystem type (detected; required for network shares).
   Each field has a short note underneath explaining what it does.
4. Leave **Preset** on *Auto*, or choose one. The note under the dropdown always tells you which preset will be used and
   the exact options it applies (for example "Auto will use “ssd” for this disk"). **Custom options** overrides the preset.
5. **Preview…**, read the diff, **Apply**.

The mount point directory is created for you. The change takes effect at the next boot, or straight away with
**Mount now** on the Mounts page.

### Plugging in a drive

While the app is open, plugging in a drive shows a banner:

- **New disk detected**: the drive has a usable filesystem and isn't in fstab. **Add to fstab…** opens the add dialog
  with the device preselected. If your desktop auto-mounted it, the banner says where.
- **Known disk reconnected**: the drive is already in fstab.
- **New disk without a filesystem**: nothing to mount yet. Format it first (for example with GNOME Disks), and it will
  be announced again.
- Removing a drive shows a short message, noting if it is still listed in fstab.

If `notify-send` is installed you also get a native desktop notification (disable with `--no-notify`).
Detection checks every two seconds and waits for the system to finish setting the disk up, so allow a moment.

### Disks

All disks and partitions as `lsblk` sees them, with size, filesystem, label, UUID, where mounted, and whether each is in fstab.

### Home links

The page has three parts.

**Key folders.** Documents, Downloads, Music, Pictures and Videos (under the names your desktop uses), plus any you add
with **Add a folder…**, for example *Claude Assistance*. Each row shows where the folder is:

- **Empty folder on this computer** or **Doesn't exist yet**: click **Link to a folder…** and choose any folder: on a
  data drive, a network share, a synced folder such as `~/Nextcloud/Documents`, or anywhere else. Type the path or use
  **Browse…**, then preview. The empty folder is replaced by a link, so apps keep using `~/Documents` while the files are
  stored in the folder you chose. If your other key folders already link into one place (say `/storage`), the dialog
  suggests `/storage/Music` and **Browse…** opens there. If you pick the top of a mounted drive (say `/mnt/data`), it suggests
  `/mnt/data/Documents` instead. A folder inside the key folder itself is refused.
- **On this computer, N items**: only an empty folder can be linked elsewhere, so nothing can get lost. Move its files
  there first (or use **New link…** with *move my existing folder there first*).
- **→ /mnt/data/Documents**: already linked. **Repoint…** changes the folder; **Make local again…** removes the link and
  puts an empty local folder back (your files stay where they are).

Folders you added show *(yours)* and have **Remove from list** (which never touches the folder). The list and your links
are saved in backups and bundles, and come back with an import after a reinstall.

**Links.** Symlinks in your home folder with a status (**ok** or **BROKEN**) and, for broken ones, the reason, such as
"`/mnt/data` is in fstab but not mounted". The **In backups** column shows whether each link is saved in snapshots and
bundles: links directly in your home folder always are; deeper ones only when you **Keep** them.

- **New link…**: enter the target (for example `/mnt/data/Music`), or click **Browse…** to pick the folder in your
  system's folder picker (it opens where drives are mounted, such as `/mnt` or `/media`). The link name defaults to the
  folder's name.
  Options: *move my existing folder there first* (`--adopt`), *create the target if missing*, *relative link*,
  *replace an existing link*, *allow a missing target*.
- **Repoint…** changes where a link points. **Remove** deletes only the link, never the data.
- Depth and hidden toggles control how far to look; by default only visible items directly in your home are listed.
  Links you keep are always listed.

**Discover links.** **Search my home folder** finds links anywhere in your home folder, up to six folders deep, such as
`~/Projects/site/assets`. Click **Keep** on the ones you want back after a reinstall (**Stop** undoes it). App caches,
browser profiles, `node_modules` and other drives mounted inside your home are skipped; tick *search hidden folders
too* to look in dot-folders, and *show links made by apps* to see links that software created.

Real files and folders are never overwritten or deleted. See [Moving a folder to a data disk](#move-a-home-folder-to-a-data-disk).

### Backups

Two lists: fstab backups (with **Diff** against the current file and **Restore…**) and home-link snapshots
(**Restore…**). **Back up now** and **Snapshot now** create manual ones. Restoring also backs up the current state first.

### Export / Import

- **Export:** the report (`.md` or `.txt`) and the portable bundle (`.json`). Each button opens your system's Save dialog
  in your Downloads folder with a suggested name (for example `fstab-bundle-myhost-2026-11-09.json`); you can choose
  another place. A note confirms where it was saved, with **Show in folder**. Bundles are saved readable only by you.
- **Import:** **Choose bundle…** opens your system's file picker; then **Preview fstab import…** and/or
  **Preview link import…**. Options: also import `/`, `/boot` and EFI (normally skipped because they are
  distribution-specific), and skip entries whose device or target is missing.

## The command line

Run `fstabctl --help`, or `fstabctl COMMAND --help` for any command. Commands that change `/etc/fstab` need root
(`sudo`); reading and previewing do not.

```sh
fstabctl scan                         # disks and which are not in fstab
fstabctl list                         # fstab entries with live status
fstabctl verify                       # errors and risky settings
```

### Adding

```sh
sudo fstabctl add                     # interactive wizard: pick a disk, mount point and preset
sudo fstabctl add /dev/sdb1 /mnt/data --preset ssd
sudo fstabctl add UUID=1234-ABCD /mnt/usb -t exfat -p windows
sudo fstabctl add nas:/export /mnt/nas -t nfs4 -p nfs
sudo fstabctl add /dev/sdb1 /mnt/data -o defaults,noatime,nofail   # custom options
fstabctl add /dev/sdb1 /mnt/data -n   # preview only, no sudo needed
```

### Changing

```sh
sudo fstabctl set /mnt/data --add-options ro
sudo fstabctl set /mnt/data --remove-options noatime
sudo fstabctl disable /mnt/usb         # keep the line, ignore it
sudo fstabctl enable /mnt/usb
sudo fstabctl remove /mnt/old
```

The target can be a mount point, a device spec, or the `#` number from `fstabctl list`.

### Mounting and repairs

```sh
sudo fstabctl mount /mnt/data          # mount one entry now, no reboot
sudo fstabctl mount --all -n           # what would be mounted, and what is skipped and why
sudo fstabctl mount --all
fstabctl verify                        # lists lines that can't be read, with their line numbers
sudo fstabctl repair --add-defaults 7  # complete "UUID=… /mnt/x ext4" with "defaults,nofail 0 0"
sudo fstabctl repair --comment-out 7   # or put a # in front of it
```

### Backups

```sh
fstabctl backup --note "before reinstall"
fstabctl backups
fstabctl diff                          # latest backup vs current
sudo fstabctl restore 20261105-1215    # an ID, a unique prefix, 'latest', or a file path
```

### Reports and bundles

```sh
fstabctl report                        # to the terminal
fstabctl report -f markdown -o fstab-report.md
fstabctl export -o bundle.json
sudo fstabctl import bundle.json -n    # preview
sudo fstabctl import bundle.json
```

### Home links

```sh
fstabctl link list
fstabctl link add /mnt/data/Music              # ~/Music -> /mnt/data/Music
fstabctl link add /mnt/data/Documents --adopt  # move ~/Documents there, link back
fstabctl link set Music /mnt/data/Music2
fstabctl link remove Music
fstabctl link check                            # exit 1 if any link is broken
fstabctl link backup; fstabctl link restore
fstabctl link import bundle.json

fstabctl link folders                          # Key folders and where each one is
fstabctl link folders add "Claude Assistance"
fstabctl link add /mnt/data/Documents Documents --replace-empty --mkdir-target   # empty ~/Documents -> drive
fstabctl link remove Documents --restore-folder # make it a local folder again
fstabctl link discover                         # find links anywhere in home
fstabctl link track Projects/site/assets       # keep it in backups and bundles
```

## Workflows

### Add a new data disk

1. Format it first (fstabctl does not partition or format).
2. `fstabctl scan` (or open **Disks** in the app) to confirm the system sees it.
3. Add it from the app, or `sudo fstabctl add /dev/sdb1 /mnt/data`.
4. Read the diff, apply, then click **Mount now** (or `sudo fstabctl mount /mnt/data`). If it mounts, it will also
   mount at boot.

### Move a home folder to a data disk

You want `~/Documents` to live on `/mnt/data` but keep working at the same path.

1. Make sure `/mnt/data` is mounted (`fstabctl list` shows **mounted**).
2. Close programs using the folder.
3. `fstabctl link add /mnt/data/Documents --adopt` (or **New link…** with *move my existing folder there first*).
4. Review the plan, apply.

It refuses if the target is under an fstab mount that is not mounted right now, because the move would put your data on the
root disk underneath the mount point, where it would vanish from view once the disk mounts. It also refuses if the
target already exists; merge by hand first. Across disks the move is copy-then-delete, so large folders take a while.

### Rebuild your system or switch distributions

Before:

```sh
fstabctl report -f markdown -o fstab-report.md   # for reading
fstabctl export -o bundle.json                   # for importing
```

Keep both somewhere that survives the reinstall (another disk, cloud, USB stick).
After installing the new system and the packages:

1. Plug in your data disks. `fstabctl scan` should list them.
2. `sudo fstabctl import bundle.json -n`, read the table:
   - **add**: disk found, entry added.
   - **add** *(re-linked by label)*: the disk was reformatted and got a new UUID, but exactly one disk has the same label.
   - **add-disabled**: disk not present; kept as a disabled entry. Enable it later with `fstabctl enable`.
   - **skip**: system mounts, or the mount point is already in the new fstab.
3. Apply, then **Mount all drives now** (or `sudo fstabctl mount --all`).
4. Once disks are mounted: `fstabctl link import bundle.json -n`, then apply. If your username changed, old
   `/home/oldname/...` targets are rewritten to the new home.

### Mount an NFS or SMB share

`sudo fstabctl add nas:/export /mnt/nas -t nfs4 -p nfs`, or for SMB `-t cifs -p cifs` and create the credentials file
(`/etc/samba/credentials`, `chmod 600`, containing `username=` and `password=` lines). The `cifs` preset points at
that path; the report warns if the file is missing or readable by others.

### Mount a USB drive

Use the `usb` preset (or let it be auto-selected for removable disks): it mounts on first access and never blocks boot.
For NTFS/exFAT/FAT drives the `windows` preset makes files owned by you.

### Undo a mistake

App: **Backups → Restore…** on the version you want. CLI: `sudo fstabctl restore latest` (or an ID).
If the machine will not boot, see [Recovering from a bad fstab](TROUBLESHOOTING.md#my-machine-wont-boot-after-an-fstab-change).

## Presets

| Preset | Options | Use for |
|---|---|---|
| `data` | `defaults,nofail` | Generic internal disk (ext4/xfs/btrfs) |
| `ssd` | `defaults,noatime,nofail` | SSD or NVMe; fewer writes. Prefer the `fstrim.timer` service over `discard` |
| `usb` | `defaults,noauto,nofail,x-systemd.automount,x-systemd.device-timeout=5,x-systemd.idle-timeout=60` | Removable drives: mounted on first use, unmounted when idle |
| `windows` | `defaults,nofail,uid=<you>,gid=<you>,dmask=022,fmask=133` | NTFS, exFAT, FAT: files owned by you, no execute bit on files |
| `nfs` | `defaults,_netdev,nofail,x-systemd.automount,x-systemd.mount-timeout=10` | NFS shares |
| `cifs` | `…,credentials=/etc/samba/credentials,uid=<you>,gid=<you>` | SMB/CIFS shares |

When no preset is named, one is chosen from the disk: Windows-type filesystems → `windows`, `nfs`/`nfs4` → `nfs`,
`cifs` → `cifs`, removable → `usb`, non-rotating → `ssd`, otherwise `data`. NTFS uses the in-kernel `ntfs3`
driver if present, else `ntfs-3g`.
