#!/usr/bin/env bash
# Build .deb and .rpm packages for fstabctl (CLI) and fstab-manager (desktop app).
#
#   packaging/build.sh                 # this machine's architecture
#   MAINTAINER="Name <email>" VENDOR=Name HOMEPAGE=https://… LICENSE_ID=MIT packaging/build.sh
#
# Needs: Go 1.22+, a C compiler, the GTK 3 and WebKitGTK 4.1 development files
# (Debian/Ubuntu: apt install build-essential libgtk-3-dev libwebkit2gtk-4.1-dev),
# dpkg-deb and dpkg-shlibdeps (for .deb), rpmbuild (for .rpm; apt install rpm).
# The desktop app links against WebKitGTK, so it is built natively: run this on an
# arm64 machine to get arm64 packages.
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT=$PWD
GUI_VER=$(grep -oP 'const version = "\K[^"]+' gui/util.go)
CLI_VER=$(grep -oP '^VERSION = "\K[^"]+' cli/fstabctl)
MAINT=${MAINTAINER:-csTechnologies <camerons@live.ca>}
VENDOR=${VENDOR:-csTechnologies}
HOMEPAGE=${HOMEPAGE:-https://cstevens.ca}
COPYRIGHT=${COPYRIGHT:-Cameron Stevens}
LICENSE_ID=${LICENSE_ID:-MIT}
ARCH=$(go env GOARCH)
GUI_REV=${GUI_REV:-1}   # package revisions: reset to 1 for a new upstream version,
CLI_REV=${CLI_REV:-2}   # raise when repackaging the same version (e.g. docs-only changes)
REV=1
OUT=$ROOT/dist/packages
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$OUT" "$ROOT/dist"

deb_arch() { case $1 in amd64) echo amd64;; arm64) echo arm64;; riscv64) echo riscv64;; esac; }
rpm_arch() { case $1 in amd64) echo x86_64;; arm64) echo aarch64;; riscv64) echo riscv64;; esac; }

gui_binary() {   # build (or reuse) the desktop app for this architecture
  local bin=$ROOT/dist/fstab-manager-linux-$ARCH
  if [[ ! -x $bin ]]; then
    (cd gui && CGO_ENABLED=1 GOFLAGS=-mod=mod go build -buildmode=pie -tags desktop,production,webkit2_41 -trimpath \
      -ldflags "-s -w -B gobuildid" -o "$bin" .)
  fi
  echo "$bin"
}

shlib_depends() {  # Debian "Depends" for a binary, worked out from the libraries it links
  local d=$WORK/shlibs; mkdir -p "$d/debian"
  printf 'Source: fstab-manager\n\nPackage: fstab-manager\nArchitecture: any\n' > "$d/debian/control"
  # Ubuntu 24.04 / Debian 13 renamed some libraries with a "t64" suffix; accept
  # the old names too so the package also installs on Ubuntu 22.04 / Pop!_OS 22.04.
  (cd "$d" && dpkg-shlibdeps -O "$1" 2>/dev/null) | sed -n 's/^shlibs:Depends=//p' \
    | sed -E 's/(lib[a-z0-9.+-]+)t64( \([^)]*\))?/\1t64\2 | \1\2/g'
}

# ---------------------------------------------------------------- .deb ---
build_deb() {  # name version arch stagedir "depends" "recommends" "suggests" "summary" "long description" [extra control lines]
  local name=$1 ver=$2 arch=$3 st=$4 dep=$5 rec=$6 sug=$7 summary=$8 long=$9 extra=${10:-}
  mkdir -p "$st/DEBIAN" "$st/usr/share/doc/$name"
  cp "$ROOT/LICENSE" "$st/usr/share/doc/$name/copyright"
  printf 'Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/\nFiles: *\nCopyright: 2026 %s\nLicense: %s\n' "$COPYRIGHT" "$LICENSE_ID" \
    > "$st/usr/share/doc/$name/copyright"
  sed 's/^/ /' "$ROOT/LICENSE" | sed 's/^ $/ ./' >> "$st/usr/share/doc/$name/copyright"
  local when; when=$(LC_ALL=C date -R)
  printf '%s (%s-%s) unstable; urgency=medium\n\n  * Packaged release %s.\n\n -- %s  %s\n' "$name" "$ver" "$REV" "$ver" "$MAINT" "$when" \
    | gzip -9n > "$st/usr/share/doc/$name/changelog.Debian.gz"
  mkdir -p "$st/usr/share/lintian/overrides"   # CHANGELOG.md keeps its name so the docs' relative links work
  { echo "$name: wrong-name-for-upstream-changelog [usr/share/doc/$name/CHANGELOG.md]"
    echo "$name: initial-upload-closes-no-bugs [usr/share/doc/$name/changelog.Debian.gz:1]"; } > "$st/usr/share/lintian/overrides/$name"
  (cd "$st" && find usr -type f -print0 | sort -z | xargs -0 md5sum > DEBIAN/md5sums)
  local size; size=$(du -sk "$st/usr" | cut -f1)
  {
    echo "Package: $name"
    echo "Version: $ver-$REV"
    echo "Architecture: $arch"
    echo "Maintainer: $MAINT"
    echo "Installed-Size: $size"
    echo "Homepage: $HOMEPAGE"
    [[ -n $dep ]] && echo "Depends: $dep"
    [[ -n $rec ]] && echo "Recommends: $rec"
    [[ -n $sug ]] && echo "Suggests: $sug"
    [[ -n $extra ]] && echo "$extra"
    echo "Section: admin"
    echo "Priority: optional"
    echo "Description: $summary"
    echo "$long" | sed 's/^/ /; s/^ $/ ./'
  } > "$st/DEBIAN/control"
  find "$st" -type d -exec chmod 755 {} +
  dpkg-deb --root-owner-group -Zxz --build "$st" "$OUT/${name}_${ver}-${REV}_${arch}.deb" >/dev/null
  echo "built $OUT/${name}_${ver}-${REV}_${arch}.deb"
}

# ---------------------------------------------------------------- .rpm ---
build_rpm() {  # name version arch(rpm) srcdir "requires;..." "recommends;..." "suggests;..." summary description noarch(0/1) files [extra spec lines]
  local name=$1 ver=$2 arch=$3 src=$4 req=$5 rec=$6 sug=$7 summary=$8 desc=$9 noarch=${10} files=${11} extra=${12:-}
  local top=$WORK/rpm-$name-$arch; mkdir -p "$top"/{BUILD,RPMS,SRPMS,SPECS}
  {
    echo "%global debug_package %{nil}"
    echo "%global __os_install_post %{nil}"
    echo "Name: $name"; echo "Version: $ver"; echo "Release: ${REV}%{?dist}"
    echo "Summary: $summary"; echo "License: $LICENSE_ID"
    echo "Packager: $MAINT"
    echo "Vendor: $VENDOR"
    echo "URL: $HOMEPAGE"
    [[ $noarch == 1 ]] && echo "BuildArch: noarch"
    local IFS=';'; for r in $req; do echo "Requires: $r"; done
    IFS=';'; for r in $rec; do echo "Recommends: $r"; done
    IFS=';'; for r in $sug; do echo "Suggests: $r"; done
    unset IFS
    [[ -n $extra ]] && echo "$extra"
    echo "Source0: $name.install"
    echo
    echo "%description"; echo "$desc" | sed 's/^\.$//'
    echo
    echo "%prep"; echo "%setup -q -c -T"; echo "cp -a %{_sourcedir}/LICENSE %{_sourcedir}/README.md %{_sourcedir}/CHANGELOG.md %{_sourcedir}/docs ."
    echo
    echo "%install"; echo "cp -a %{_sourcedir}/root/. %{buildroot}/"
    echo
    echo "%files"; echo "%license LICENSE"; echo "%doc README.md CHANGELOG.md docs"; echo "$files"
    echo
    echo "%changelog"
    echo "* $(LC_ALL=C date '+%a %b %d %Y') $MAINT - $ver-$REV"
    echo "- Packaged release $ver"
  } > "$top/SPECS/$name.spec"
  : > "$src/$name.install"
  rpmbuild -bb --quiet --target "$arch" --define "_topdir $top" --define "_sourcedir $src" \
    --define "_rpmdir $OUT" --define "_build_name_fmt %%{NAME}-%%{VERSION}-%%{RELEASE}.%%{ARCH}.rpm" \
    "$top/SPECS/$name.spec"
  echo "built $OUT/$name-$ver-$REV.$arch.rpm"
}

install_docs() {  # destdir: README, CHANGELOG and the docs/ folder
  install -Dm644 README.md "$1/README.md"
  install -Dm644 CHANGELOG.md "$1/CHANGELOG.md"
  for f in docs/*.md; do install -Dm644 "$f" "$1/docs/$(basename "$f")"; done
}

stage_gui() {  # root [nodoc]
  local root=$1
  install -Dm755 "$(gui_binary)" "$root/usr/bin/fstab-manager"
  ln -s fstab-manager "$root/usr/bin/fstabctl-gui"            # old name, kept for scripts and habits
  install -Dm644 packaging/fstab-manager.desktop "$root/usr/share/applications/fstab-manager.desktop"
  install -Dm644 packaging/org.fstabctl.manager.policy "$root/usr/share/polkit-1/actions/org.fstabctl.manager.policy"
  for s in 16 24 32 48 64 128 256 512; do
    install -Dm644 "packaging/icons/fstab-manager-$s.png" "$root/usr/share/icons/hicolor/${s}x${s}/apps/fstab-manager.png"
  done
  install -Dm644 packaging/icons/fstab-manager.svg "$root/usr/share/icons/hicolor/scalable/apps/fstab-manager.svg"
  install -Dm644 packaging/icons/fstab-manager-symbolic.svg "$root/usr/share/icons/hicolor/symbolic/apps/fstab-manager-symbolic.svg"
  gzip -9n < packaging/fstab-manager.1 > "$WORK/fstab-manager.1.gz"
  install -Dm644 "$WORK/fstab-manager.1.gz" "$root/usr/share/man/man1/fstab-manager.1.gz"
  ln -s fstab-manager.1.gz "$root/usr/share/man/man1/fstabctl-gui.1.gz"
  [[ ${2:-} == nodoc ]] || install_docs "$root/usr/share/doc/fstab-manager"
}
stage_cli() {  # root [nodoc]
  local root=$1
  install -Dm755 cli/fstabctl "$root/usr/bin/fstabctl"
  sed -i '1s|.*|#!/usr/bin/python3|' "$root/usr/bin/fstabctl"   # distro policy: no "env" in shebangs
  gzip -9n < packaging/fstabctl.1 > "$WORK/fstabctl.1.gz"
  install -Dm644 "$WORK/fstabctl.1.gz" "$root/usr/share/man/man1/fstabctl.1.gz"
  [[ ${2:-} == nodoc ]] || install_docs "$root/usr/share/doc/fstabctl"
}

GUI_DESC="fstab Manager is a desktop app for managing /etc/fstab. It shows your
mounts with live status, explains problems in plain language and offers
one-click fixes, and mounts drives without a reboot. Disks are added with
sensible presets (SSD, USB, NTFS/exFAT, NFS, CIFS), and when a drive is
plugged in it offers to add it.
.
Every change is previewed as a diff, validated and backed up before
anything is written. The app also manages symlinks from your home folder
to your data disks.
.
It runs in its own window using the system's WebKitGTK (no browser, no
network port). Writing /etc/fstab asks for administrator rights through
polkit."
CLI_DESC="fstabctl lists, adds, edits, backs up, restores, verifies and exports
fstab entries, with a human-readable report and a portable bundle for
rebuilding a system or switching distributions. It also manages
home-folder symlinks.
.
Every change is validated, shown as a diff and backed up first. Install
fstab-manager for the desktop app."

# --- CLI package (architecture independent)
REV=$CLI_REV
st=$WORK/deb-cli; stage_cli "$st"
build_deb fstabctl "$CLI_VER" all "$st" "python3 (>= 3.8)" "fstab-manager" "" \
  "manage, back up and export fstab and home-folder links" "$CLI_DESC"
src=$WORK/rpmsrc-cli; mkdir -p "$src/root"; stage_cli "$src/root" nodoc; cp LICENSE README.md CHANGELOG.md "$src/"; cp -r docs "$src/"
build_rpm fstabctl "$CLI_VER" noarch "$src" "python3 >= 3.8;util-linux" "fstab-manager" "" \
  "Manage, back up and export fstab and home-folder links" "$CLI_DESC" 1 \
  "%{_bindir}/fstabctl
%{_mandir}/man1/fstabctl.1*"

# --- GUI package (this machine's architecture)
REV=$GUI_REV
st=$WORK/deb-gui; stage_gui "$st"
DEPS=$(shlib_depends "$st/usr/bin/fstab-manager")
[[ -n $DEPS ]] || { echo "dpkg-shlibdeps found no library dependencies" >&2; exit 1; }
build_deb fstab-manager "$GUI_VER" "$(deb_arch "$ARCH")" "$st" "$DEPS" \
  "pkexec | policykit-1, xdg-utils, mount" "fstabctl, libnotify-bin" \
  "desktop app for managing fstab mounts, backups and home links" "$GUI_DESC" \
  "Provides: fstabctl-gui
Replaces: fstabctl-gui (<< $GUI_VER)
Breaks: fstabctl-gui (<< $GUI_VER)"
src=$WORK/rpmsrc-gui; mkdir -p "$src/root"; stage_gui "$src/root" nodoc; cp LICENSE README.md CHANGELOG.md "$src/"; cp -r docs "$src/"
build_rpm fstab-manager "$GUI_VER" "$(rpm_arch "$ARCH")" "$src" "util-linux;hicolor-icon-theme" "polkit;xdg-utils" "fstabctl;libnotify" \
  "Desktop app for managing fstab mounts, backups and home links" "$GUI_DESC" 0 \
  "%{_bindir}/fstab-manager
%{_bindir}/fstabctl-gui
%{_datadir}/applications/fstab-manager.desktop
%{_datadir}/polkit-1/actions/org.fstabctl.manager.policy
%{_datadir}/icons/hicolor/*/apps/fstab-manager*
%{_mandir}/man1/fstab-manager.1*
%{_mandir}/man1/fstabctl-gui.1*" \
  "Provides: fstabctl-gui = %{version}-%{release}
Obsoletes: fstabctl-gui < $GUI_VER"
echo; ls -1 "$OUT"
