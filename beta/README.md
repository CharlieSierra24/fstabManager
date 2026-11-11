# Beta packages

Ready-to-install packages of **fstab Manager 0.6.6** (desktop app) and **fstabctl 0.6.1** (command-line tool).

> **Beta.** These builds are tested on Ubuntu 24.04 (x86-64) and used on Pop!_OS 24.04, but not yet across
> every distribution, desktop and graphics driver. fstab Manager edits `/etc/fstab`, a file your computer needs to
> start. It always shows a preview and makes a backup before any change. Read
> [If something goes wrong](../README.md#if-something-goes-wrong) before you start, and please report problems in
> [Issues](https://github.com/CharlieSierra24/fstabManager/issues).

| File | For |
| --- | --- |
| [`fstab-manager_0.6.6-2_amd64.deb`](fstab-manager_0.6.6-2_amd64.deb) | Desktop app: Pop!_OS, Ubuntu, Debian, Linux Mint (x86-64) |
| [`fstabctl_0.6.1-3_all.deb`](fstabctl_0.6.1-3_all.deb) | Command-line tool: Pop!_OS, Ubuntu, Debian, Linux Mint |
| [`fstab-manager-0.6.6-2.x86_64.rpm`](fstab-manager-0.6.6-2.x86_64.rpm) | Desktop app: Fedora, openSUSE (x86-64) |
| [`fstabctl-0.6.1-3.noarch.rpm`](fstabctl-0.6.1-3.noarch.rpm) | Command-line tool: Fedora, openSUSE |
| [`SHA256SUMS`](SHA256SUMS) | Checksums to confirm your download |

## Install

Download the files for your system (open each file above, then **Download raw file**), then in a terminal:

**Pop!_OS, Ubuntu, Debian, Linux Mint**

```sh
cd ~/Downloads
sudo apt install ./fstab-manager_0.6.6-2_amd64.deb ./fstabctl_0.6.1-3_all.deb
```

**Fedora, openSUSE**

```sh
cd ~/Downloads
sudo dnf install ./fstab-manager-0.6.6-2.x86_64.rpm ./fstabctl-0.6.1-3.noarch.rpm
```

The package manager adds anything missing, such as the system's WebKitGTK library. Then open **fstab Manager** from
your application menu.

**Check a download (optional):** put `SHA256SUMS` in the same folder and run `sha256sum -c --ignore-missing SHA256SUMS`.
Each file should say `OK`.

## Requirements

- x86-64 (64-bit Intel or AMD). ARM builds aren't available yet.
- The desktop app needs GTK 3 and WebKitGTK 4.1: Ubuntu 22.04 or newer, Debian 12 or newer, a current Fedora or openSUSE.
- The command-line tool needs Python 3.8 or newer.
