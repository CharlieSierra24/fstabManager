# Troubleshooting

## Contents

- [My machine won't boot after an fstab change](#my-machine-wont-boot-after-an-fstab-change)
- [The app says "read-only"](#the-app-says-read-only)
- [No password dialog appears](#no-password-dialog-appears)
- [The window doesn't open, or stays blank](#the-window-doesnt-open-or-stays-blank)
- ["Mount now" fails](#mount-now-fails)
- [A finding's fix button is refused](#a-findings-fix-button-is-refused)
- [A plugged-in drive isn't detected](#a-plugged-in-drive-isnt-detected)
- [A change was applied but nothing is mounted](#a-change-was-applied-but-nothing-is-mounted)
- [A home link is broken](#a-home-link-is-broken)
- [`--adopt` refuses to run](#--adopt-refuses-to-run)
- [Refusing to write: duplicate mount point](#refusing-to-write-duplicate-mount-point)
- [NTFS drive is read-only or won't mount](#ntfs-drive-is-read-only-or-wont-mount)
- [Where are my backups?](#where-are-my-backups)
- [`fstabctl gui` says the app is a separate program](#fstabctl-gui-says-the-app-is-a-separate-program)
- [SELinux denials](#selinux-denials)

## My machine won't boot after an fstab change

An entry for a missing or wrong device, without `nofail`, can make systemd wait and then drop to an emergency shell.

**If you reach an emergency or rescue shell:**

1. Log in with the root password (or choose the rescue entry in the boot menu).
2. Make the root filesystem writable: `mount -o remount,rw /`
3. Restore a backup, or edit the bad line (`fstabctl restore latest` works here too, if it is installed):
   ```sh
   ls /home/*/.local/share/fstabctl/backups/      # find the newest fstab-<ID>.bak
   cp /home/<you>/.local/share/fstabctl/backups/fstab-<ID>.bak /etc/fstab
   # or: nano /etc/fstab   and put a # in front of the problem line
   ```
4. `systemctl daemon-reload` then `reboot`.

**If you can't log in at all:** boot a live USB, mount your root partition (for example `sudo mount /dev/sda2 /mnt`), then
fix `/mnt/etc/fstab` the same way. Backups are under `/mnt/home/<you>/.local/share/fstabctl/backups/` unless your home is
on another disk.

**Prevention:** keep `nofail` on every non-essential mount (the presets do), read the diff, and use **Mount now** (or
`sudo fstabctl mount --all`) right after applying: it reports errors while you still have a working system. Also look at
the Overview's findings before rebooting: anything marked **PROBLEM** or **WARNING** is worth fixing first.

## The app says "read-only"

The app could not find a way to get administrator rights. It looks for, in order: write access to the file (running as
root), `pkexec` with a graphical session (`DISPLAY` or `WAYLAND_DISPLAY` set), and passwordless `sudo -n`.

- Install polkit's `pkexec` (`pkexec` or `policykit-1` on Debian/Ubuntu, `polkit` on Fedora) and restart the app, **or**
- make the change with the command line instead: `sudo fstabctl …`.

Home-link features work without any of this.

## No password dialog appears

`pkexec` needs a polkit authentication agent running in your desktop session (GNOME, KDE, XFCE and most desktops start one;
minimal window managers may not). Check with `pgrep -a polkit`. Install and autostart an agent such as
`polkit-gnome-authentication-agent-1`, or make the change with `sudo fstabctl …`. Over SSH without a display, use the CLI.

## The window doesn't open, or stays blank

- Run `fstab-manager` in a terminal to see its messages.
- **Missing libraries** (`error while loading shared libraries: libwebkit2gtk-4.1.so.0`): install WebKitGTK 4.1
  (`libwebkit2gtk-4.1-0` on Debian/Ubuntu/Pop!_OS, `webkit2gtk4.1` on Fedora). Installing the package with `apt`/`dnf`
  does this for you.
- **Blank or white window.** This is usually a graphics-driver problem in WebKitGTK. With NVIDIA's proprietary driver the
  app already applies the standard fix. Otherwise, try:
  ```sh
  WEBKIT_DISABLE_DMABUF_RENDERER=1 fstab-manager
  WEBKIT_DISABLE_COMPOSITING_MODE=1 fstab-manager     # if the first doesn't help
  ```
  If one works, you can add it to the `Exec=` line of a copy of the launcher in `~/.local/share/applications/`.
- **Nothing happens when you start it again**: only one window runs at a time; look for the existing window (it is
  brought forward, but some desktops only flash its taskbar entry).
- Under `sudo` without a desktop session the window cannot open. Run it as your normal user.

## "Mount now" fails

The dialog shows `mount`'s own message. Common ones:

| Message contains | Meaning and fix |
|---|---|
| `wrong fs type, bad option, bad superblock` | The filesystem type or an option doesn't suit the disk. Check the type on the Disks tab and remove unusual options with **Edit**. For NTFS, see [below](#ntfs-drive-is-read-only-or-wont-mount) |
| `can't find UUID=…` / `special device … does not exist` | The disk isn't connected, or the UUID is wrong. Compare with the Disks tab |
| `unknown filesystem type` | The driver isn't installed (for example `exfatprogs`, `ntfs-3g`, `nfs-common`, `cifs-utils`) |
| `mount point does not exist` | Rare; the folder is created automatically. Check that the path is right |
| `Permission denied` / `needs administrator rights` | The password dialog was cancelled, or no way to get rights; see [read-only](#the-app-says-read-only) |

## A finding's fix button is refused

"fstab has changed since this was shown": fstab was edited after the page last loaded (by you in a terminal, by
another tool, or by an earlier fix). The action is refused so it can't change a different line. Click another tab and
back, or wait a moment for the page to refresh, and use the new button.

## A plugged-in drive isn't detected

- The watcher polls every two seconds and waits for the system to settle, so allow a few seconds.
- It lists what `lsblk` lists. Loop devices, optical drives and zram are ignored. Run `lsblk -f` to see whether the
  system itself has the disk. In a virtual machine, make sure the USB device is attached to the guest.
- A disk with no filesystem produces a "needs formatting" banner rather than an "Add" button.
- The chip in the header should read **● watching disks**. If it doesn't, close and reopen the app.
- If your desktop auto-mounted the drive, the banner says where. You can still add it to fstab for a permanent location.

## A change was applied but nothing is mounted

Applying edits the file only. To mount without rebooting, click **Mount now** on the entry (Mounts page) or
**Mount all drives now** (Overview), or:

```sh
sudo fstabctl mount --all        # reloads systemd's view of fstab, then mounts what should be mounted
```

If it fails, see ["Mount now" fails](#mount-now-fails).

Entries from the `usb` and network presets use `noauto`/`x-systemd.automount`, so they mount the first time something
accesses the path (open the folder), not at boot.

## A home link is broken

`fstabctl link check` (or the **Home links** tab) says why. The common cause is that the target's disk is not mounted:
mount it (`sudo mount /mnt/data`) and the link works again. If the disk is mounted but the path is missing, the folder was
moved or renamed: repoint the link (`fstabctl link set NAME NEWTARGET`) or recreate the folder.
After a rebuild, run `fstabctl link import bundle.json` only once the disks are mounted.

## `--adopt` refuses to run

| Message | Meaning and fix |
|---|---|
| "…is under /mnt/data, which is in fstab but not mounted" | The move would write onto the root disk beneath the mount point. Mount the disk first |
| "…already exists; merge the two by hand" | The target folder already exists. Merge the contents manually, then run again |
| "…does not exist; is the disk mounted?" | The parent of the target is missing. Mount the disk or create the parent |
| "…is a mount point; refusing to move it" | You pointed it at something that is itself mounted |
| "…already exists as a real directory" (without `--adopt`) | Real files are never overwritten. Add `--adopt`, or choose another name |

## Refusing to write: duplicate mount point

Two enabled entries would use the same mount point. Pick another mount point, or disable/remove the old entry first.
Pre-existing problems in your fstab never block unrelated changes; only problems a change would introduce do.

## NTFS drive is read-only or won't mount

- If Windows hibernated or "Fast Startup" left the volume marked dirty, Linux mounts it read-only or refuses.
  Fully shut down Windows (or disable Fast Startup) and try again.
- fstabctl uses the in-kernel `ntfs3` driver when available and `ntfs-3g` otherwise. If the driver is missing, install
  `ntfs-3g`.
- The `windows` preset sets ownership to you; check the `uid=`/`gid=` options if files appear owned by root.

## Where are my backups?

`~/.local/share/fstabctl/backups/` of the user who ran the tool (the invoking user even under `sudo`).
List them with `fstabctl backups` or the **Backups** tab. Set `FSTABCTL_BACKUP_DIR` to store them elsewhere,
for example on a data disk that survives reinstalls.

## `fstabctl gui` says the app is a separate program

The CLI only launches the desktop app; it does not contain it. Install the `fstab-manager` package, or put the
`fstab-manager` program next to the script or in your `PATH`.

## SELinux denials

After writing, fstabctl runs `restorecon` on `/etc/fstab` when it exists, so the file keeps the correct label. If a
mount is denied, check `ausearch -m avc -ts recent`; mount points under unusual paths may need an appropriate
file context (`semanage fcontext`).
