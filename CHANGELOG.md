# Changelog

Versions are per component: **CLI** is `fstabctl` (Python), **GUI** is fstab Manager, `fstab-manager` (Go; called
`fstabctl-gui` up to 0.3.0).

## 2026-11-11: repository on GitHub

### Changed
- The project now lives at https://github.com/CharlieSierra24/fstabManager. The README is the user guide, with a short
  introduction and install instructions at the top, and screenshots in `docs/images/`.
- Copyright is held by Cameron Stevens (`LICENSE`, and the packages' copyright notice from the next build).
  csTechnologies stays the brand, package maintainer and About author.

## 2026-11-11: fstab Manager 0.6.6 (package -1)

### Fixed
- The About/splash close button (×) overlapped the logo banner. The box now has room at the top for it, and it no
  longer shows a focus ring when the box opens (Tab still reaches it, with the ring; Esc still closes the box).

## 2026-11-11: packages fstab Manager 0.6.5-2, CLI 0.6.1-2

### Changed
- Package developer information now matches About: maintainer **csTechnologies <camerons@live.ca>**, homepage
  **https://cstevens.ca** (shown by `apt show` and software centres), RPM vendor and URL, the copyright notice and
  `LICENSE` (Copyright 2026 csTechnologies), and the polkit rule's vendor. No changes to the programs.

## 2026-11-10: fstab Manager 0.6.5 (package -1)

### Changed
- The version and **?** now sit in a footer bar across the bottom of the window, with its own background, so they
  never overlap the page. Pages leave room for it, and notices appear above it.
- The **?** (About) button is larger and blue, so it's easy to spot.

## 2026-11-10: fstab Manager 0.6.4 (package -1)

### Fixed
- **Dark mode:** the splash/About box and other dialogs blended into the app behind them. Dialogs now use a lighter
  raised surface with a visible edge over a darker, blurred backdrop, and About has an accent line along its top.
- **Dark mode:** dropdowns (Device, Preset, depth) were drawn bright white with hard-to-read text. They now follow the
  app's theme, and form controls follow the system's light or dark setting.

## 2026-11-10: fstab Manager 0.6.3 (package -1)

### Fixed
- Saving a report or bundle failed with "default directory '…/Downloads' does not exist" when `~/Downloads` is a
  link to another folder (for example `/storage/Downloads`). The window framework (Wails) checks the dialog's starting
  folder without following links; the app now passes the real folder behind the link. The same fix covers the
  **Choose bundle…** and **Browse…** dialogs.

## 2026-11-10: fstab Manager 0.6.2 (package -1)

### Changed
- About and the splash screen show **Author: csTechnologies · cstevens.ca** instead of an email address; clicking
  cstevens.ca opens https://cstevens.ca in your browser.

## 2026-11-10: fstab Manager 0.6.1, CLI 0.6.1 (packages -1)

### Changed
- Key folders can link to **any folder**, not only one on a drive: a network share, a synced folder (Nextcloud,
  Dropbox…), or anywhere else. The button is now **Link to a folder…**, and its dialog explains the choices and offers
  **Browse…**.
- The "use /mnt/data/Documents instead" suggestion only appears when you pick the top of a mounted drive.
- When your other key folders already link into one place (say `/storage/Documents`), the dialog suggests the same
  place for this one (`/storage/Music`), and **Browse…** opens there.

### Fixed
- A link can no longer point to a folder inside itself (for example `~/Documents` → `~/Documents/archive`). Both
  programs refuse this.

## 2026-11-10: fstab Manager 0.6.0, CLI 0.6.0 (packages -1)

### Added
- **Key folders** on the Home links page: Documents, Downloads, Music, Pictures and Videos (with your desktop's folder
  names), plus your own via **Add a folder…** (for example “Claude Assistance”). An empty or missing folder can be
  pointed at a folder on a drive with **Point to a drive…**; **Make local again…** undoes it. Folders with files are
  never replaced. Picking a drive's top folder suggests a subfolder named after the key folder.
- **Discover links**: searches your whole home folder (up to six folders deep) for links you made, skipping app data and
  other drives, and flags links probably made by apps. **Keep** saves a link in backups and bundles.
- CLI: `link discover`, `link track` / `untrack`, `link folders [add|remove]`, `link add --replace-empty`,
  `link remove --restore-folder`.
- Settings file `~/.config/fstabctl/settings.json` (Key folders and tracked links), shared by both programs and
  carried in bundles (`key_folders`, `tracked_links`).

### Fixed
- Importing or restoring links after a reinstall skipped folders like `~/Documents` because the fresh install had
  created them empty. Empty folders are now replaced by the link; folders with files are still skipped.
- Snapshots and bundles only kept links directly in your home folder; links you keep (any depth) are now included.

## 2026-11-10: fstab Manager 0.5.1 (package -1)

### Added
- **Browse…** next to the target field in **New link…** and **Repoint…**: picks the folder with the system's folder
  picker, starting at what you typed or where drives are mounted (`/mnt`, `/media/<you>`, `/run/media/<you>`).
- The link-name field shows the name it will use (for example “Music”) as soon as a target is chosen.

## 2026-11-10: fstab Manager 0.5.0 (package -1)

### Added
- **Splash screen** at startup with the logo, version and author. It closes after 15 seconds, with a
  countdown bar, or with the **×** in its top-right corner or Esc.
- **About**: the same box, opened with the new **?** button in the bottom-left corner next to the version. No countdown.

## 2026-11-09: fstab Manager 0.4.0, CLI 0.5.0 (packages: GUI -1, CLI -1)

### Changed
- GUI: **a standalone app in its own window.** fstab Manager now uses Wails with the system's WebKitGTK instead of a
  local web server shown in a browser. There is no network port, no token, no idle timeout, and no leftover tab: **Quit**
  or closing the window ends the app. Only one window runs at a time.
- GUI: renamed to **fstab Manager**: program `fstab-manager`, package `fstab-manager` (replaces `fstabctl-gui`; the old
  command remains as a link). New icon in the menu, window and taskbar; the version is shown in the bottom-left corner.
- GUI: reports and bundles are saved with the system's **Save dialog**, opening in your Downloads folder, with an
  in-window confirmation and **Show in folder**. This replaces browser downloads, whose notifications could appear
  off-screen. Bundles are saved readable only by you.
- GUI: bundles to import are chosen with the system's **Open dialog**.
- GUI: the header and tabs stay visible while scrolling; notices no longer cover open dialogs.
- Packages: the desktop app now depends on GTK 3 and WebKitGTK 4.1 (installed automatically). Built for x86-64; ARM
  packages must be built on ARM hardware.

### Added
- **Findings explain themselves and offer fixes.** Each one says what's wrong, why it matters and what to do, with
  buttons (Add nofail, Disable entry, Add _netdev, Remove discard, Switch to UUID, Mount now, Add default options,
  Comment out line N). Each opens the usual preview first. The report includes the explanations too.
- **Mount without rebooting**: **Mount now** on entries that aren't mounted, **Mount all drives now**, and
  `fstabctl mount TARGET | --all` (reloads systemd's view of fstab first, explains every skip and failure).
- **Repair broken lines**: `fstabctl repair --comment-out LINE | --add-defaults LINE`, also offered as buttons.
- `--expect SPEC` for `set`, `remove`, `enable`, `disable` and `mount`: the action is refused if fstab changed since it
  was shown, so a fix can never land on the wrong line. The app always uses it.
- A missing disk for `/`, `/boot` or EFI is reported as a problem to check before rebooting (instead of suggesting `nofail`).
- With NVIDIA's proprietary driver, the app turns off WebKitGTK's DMA-BUF renderer to avoid a blank window.

### Fixed
- Quoted values such as `UUID="1234-ABCD"` (as `blkid` prints them) were reported as malformed lines. Both programs now
  read them correctly, and drive detection recognises those disks.
- Failed actions showed only "Failed"; the reason is now shown.
- The leftover "fstabctl has stopped. You can close this tab." page is gone.

## 2026-11-09: GUI 0.3.0 (interim build)

An interim rebuild from the 0.2.1 source with the quoted-UUID fix, the new name in the window and the version label.
Superseded by 0.4.0.

## 2026-11-07: GUI 0.2.1 (packages: GUI -1, CLI -3)

### Fixed
- GUI: the **Add a mount** dialog layout. When the "Network path" row was hidden, its label stayed behind and every field
  below it shifted one cell, so labels and inputs no longer lined up. The Preset dropdown was also squeezed into the label
  column, truncating "Auto (recommended)". Hidden rows now hide label and field together, and fields fill their column.

### Changed
- GUI: every field in the Add and Edit dialogs now has a short explanatory note. The Preset note says which preset **Auto**
  will pick for the selected disk and shows the exact options; preset names are described ("SSD / NVMe (ssd)").
- Packages: the CLI packages move to revision `-3` only because the bundled documentation changed; the program is unchanged (0.4.0).

## 2026-11-05: CLI 0.4.0, GUI 0.2.0, packages revision 2

### Added
- GUI: **live disk detection.** A plugged-in drive raises a banner with "Add to fstab…"; known disks are recognised,
  removals are reported, and disks without a filesystem are flagged. Optional native notification via `notify-send`
  (`--no-notify` disables it). A header chip shows whether the live feed is connected.
- GUI: polkit policy `org.fstabctl.gui.apply`, so the password prompt describes what is being changed.
- GUI: `--no-notify` option; the page keeps the server alive while it is open, and the server exits after
  10 minutes with no page open (`--idle`).
- Packages: `.deb` (amd64, arm64, all) and `.rpm` (x86_64, aarch64, noarch) for both components, with man pages,
  a desktop launcher and the full documentation set. `packaging/build.sh` rebuilds them.
- Documentation: user guide, reference, security notes, troubleshooting and developer notes.

### Changed
- The privileged re-exec used by the GUI passes its settings as arguments instead of environment variables,
  because `pkexec` clears the environment.

### Fixed
- Backups or link snapshots created within the same second sorted in the wrong order, so "latest" could pick the
  older one. Fixed in both the CLI and the GUI.
- CLI: `fstabctl add UUID=…` without `-t` could write the filesystem type as `None`. It now detects the type from
  the disk or asks for `-t`.

### Notes
- Package revision `-2` is the first with documentation; `-1` builds were never published.

## Earlier development (not separately released)

- **GUI 0.1.0.** The desktop app moved from a Python-served web page to a Go program with the same interface,
  as one static binary with no runtime dependencies. It reimplements the CLI's logic natively and uses the same
  backup, snapshot and bundle formats, verified in both directions. The CLI's `gui` command became a launcher.
- **CLI 0.3.0.** Web-based GUI (`fstabctl gui`) served by the Python script.
- **CLI 0.2.0.** Home-folder symlink management (`link` commands), including `--adopt`, snapshots, and bundle
  import with home-directory remapping.
- **CLI 0.1.0.** fstab management: `list`, `scan`, `add` (with presets and an interactive wizard), `set`,
  `remove`, `enable`, `disable`, `backup`, `backups`, `diff`, `restore`, `verify`, `report`, `export`, `import`, `presets`.
