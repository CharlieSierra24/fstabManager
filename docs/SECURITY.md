# Security notes

What the tools can do, how privileges are handled, and what is **not** protected.
fstab Manager and fstabctl edit a file that can stop a machine booting, so the design aims to make mistakes visible and
reversible, and to keep administrator rights narrow.

## Privilege model

- **The CLI** (`fstabctl`) runs as you. Commands that change `/etc/fstab` or mount drives need `sudo`. Previews (`-n`),
  reading, reports and all home-link operations do not.
- **The desktop app** (`fstab-manager`) runs as you, not root. Only the final step of a change (writing `/etc/fstab`, or
  mounting) is elevated, by re-running the same program through `pkexec` (your desktop's password dialog), or `sudo -n`
  if passwordless sudo is configured, using a fixed internal command (`fstab-manager exec`). If neither is available the
  app is read-only.
- **Polkit policy.** The packages install `org.fstabctl.manager.apply`, bound to `/usr/bin/fstab-manager`, requiring
  administrator authentication (remembered briefly for the active session). A copy of the program outside `/usr/bin`
  falls back to polkit's generic "run a program as another user" prompt, which still requires administrator authentication.
- **What the elevated helper does:** it runs one operation from a fixed list (`add`, `remove`, `enable`, `disable`, `set`,
  `restore`, `import`, `mount`, `repair`), validated like any CLI call. It cannot be told to write arbitrary files: the app
  refuses `--fstab` and `--home` options coming from the page, and import files must come from the app's own private folder.
- **Running the whole app as root** (`sudo fstab-manager`) is not recommended; see [Known limitations](#known-limitations).

## How the window talks to the program

fstab Manager is a native window (Wails, using the system's WebKitGTK). The page inside it calls the program
**in-process**: there is no web server, no network port and no token. Nothing outside the app can send it commands.

| Concern | How it's handled |
|---|---|
| Another machine, or a web page in your browser, reaching the app | Impossible: the app opens no network port. Requests come only from its own window |
| Abusing the command interface | Fixed allow-list of operations; unknown or extra commands refused; `-y`/`-n`/`--fstab`/`--home` from the page are stripped or rejected. Changes must be JSON POSTs |
| Acting on the wrong line after fstab changed | Fix buttons and row actions send `--expect SPEC` (or the line's text for repairs); the operation is refused if the entry no longer matches |
| Reading arbitrary files via import | You choose a bundle in the system's Open dialog; it is checked and copied to a private temporary folder, and only files there can be imported |
| Saving files | You choose the location in the system's Save dialog. "Show in folder" works only for files the app saved in this session |
| Content injection into the page (for example a malicious disk label) | The page builds its DOM from text and never uses `innerHTML`; a Content-Security-Policy confines it to its own content (inline script is allowed because the page is a single file) |
| Two copies fighting over fstab | Only one window runs at a time; starting the app again brings the existing window forward |

## What gets written, and where

- `/etc/fstab` (atomic replace, original mode and owner kept) and the mount point directories you ask for.
- Backups, snapshots, reports and bundles you create, in your home folder or where you choose. Exported bundles are
  saved readable only by you (mode `600`).
- Symlinks in your home folder, and, for `--adopt`, the folder you chose to move.
- **Never** written: credentials, passwords, or file contents from your disks. The `cifs` preset only *references*
  `/etc/samba/credentials`; creating that file (mode `600`) is up to you.

## Safety nets against mistakes

- Diff preview before every write, and a plan before every mount; nothing is applied without confirmation (`-y` or **Apply**).
- Automatic backup before each write, stored outside `/etc`.
- Validation refuses changes that would add malformed lines or duplicate mount points.
- Boot-critical mounts (`/`, `/boot`, `/boot/efi`, `/efi`) cannot be removed or disabled without `--force` (CLI only),
  and "Mount all" never touches them.
- Presets include `nofail`, so a missing disk does not block boot.
- Repairs only touch lines that can't be read, and only the line you were shown.
- Home links: only symlinks are ever removed or replaced; link names cannot escape your home; `--adopt` refuses
  to write under an unmounted fstab mount.

## Privacy of exported files

Reports and bundles contain your hostname, distribution and kernel, disk models, labels and UUIDs, mount points,
link names, and the full text of fstab. They do not contain secrets, but they describe your system. Review before sharing
publicly.

## Known limitations

- **Root mode removes the password prompt.** If you start the app with `sudo`, every change is applied without asking.
  Start it as your normal user and let polkit ask when needed.
- **Local malware running as you** can do anything you can; the app cannot defend against that.
- **fstab itself is powerful.** A wrong but valid entry can still make boot slow or drop to an emergency shell. The
  diff, `nofail` and backups reduce this; they do not remove it. See
  [Troubleshooting](TROUBLESHOOTING.md#my-machine-wont-boot-after-an-fstab-change).
- The polkit dialog and desktop notifications depend on your session; they have not been exercised on every desktop.

## Reporting problems

Open an issue with the project you received this from, or contact whoever maintains your copy, including the output
of `fstabctl --version`, `fstab-manager --version` and your distribution. Do not post bundles or reports unreviewed.
