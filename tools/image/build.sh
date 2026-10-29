#!/bin/sh
# Make a keelNAS system disk. Runs as root on FreeBSD 15.x.
#
#   build.sh DISK VERSION [AUTHORIZED_KEYS]        (tools/image/files next to it as ./files)
#
# Optional: ./files/keelnasd, the binary from keelnasd/build.sh, becomes /usr/local/sbin/keelnasd
# (with the link keelnas); without it the image has no keelnasd.
#
# DISK is a whole disk (vtbd1) that becomes the image: an ESP, swap and a ZFS pool "knsys" with
# one boot environment per version (knsys/ROOT/VERSION). The system comes from FreeBSD's base
# packages (pkgbase) and Samba and smartmontools from our own package repository (docs/05
# section 11: built with our options by tools/pkgrepo/build.sh, signed with the key whose public
# half is ./files/keelnas-pkg.pub); nothing is compiled here. PKGREPO is that repository's
# directory (default the one tools/pkgrepo/build.sh fills on this machine).
# With AUTHORIZED_KEYS it is a test build: root logs in over ssh with those keys and with the
# password "keel" on the console.
set -eu

DISK=$1
VER=$2
KEYS=${3:-}
POOL=knsys
MNT=/mnt/kn
FILES=$(dirname "$0")/files   # copied next to this script
# Not FreeBSD-set-minimal but what of it a NAS uses (docs/05 section 11): no locales, rescue,
# firmware-iwm, at, ppp, wpa, powerd, hyperv-tools, nuageinit, periodic, vi, mandoc, kernel-man,
# pkg-bootstrap, devmatch, fwget, ufs (the root is ZFS, the ESP is msdosfs in the kernel). pkg
# brings back what a kept package needs (libmagic and ufs-lib come back that way).
# FreeBSD-libexecinfo: Samba's libraries need libexecinfo.so. FreeBSD-nvme-tools: nvmecontrol,
# which keelnasd reads NVMe health with (smartctl cannot open nda, docs/04 section 15); not in
# set-minimal either.
BASE="FreeBSD-runtime FreeBSD-clibs FreeBSD-rc FreeBSD-devd FreeBSD-dhclient FreeBSD-geom
 FreeBSD-zfs FreeBSD-zfs-lib FreeBSD-ctl FreeBSD-ssh FreeBSD-bootloader FreeBSD-efi-tools
 FreeBSD-caroot FreeBSD-certctl FreeBSD-cron FreeBSD-newsyslog FreeBSD-syslogd FreeBSD-resolvconf
 FreeBSD-pam FreeBSD-pam-lib FreeBSD-zoneinfo FreeBSD-openssl FreeBSD-openssl-lib FreeBSD-zlib
 FreeBSD-xz FreeBSD-xz-lib FreeBSD-zstd FreeBSD-zstd-lib FreeBSD-bzip2 FreeBSD-bzip2-lib
 FreeBSD-ncurses FreeBSD-ncurses-lib FreeBSD-utilities FreeBSD-fetch FreeBSD-vt-data
 FreeBSD-libexecinfo FreeBSD-nvme-tools FreeBSD-kernel-generic"
PORTS="samba422 smartmontools"
PKGREPO=${PKGREPO:-/usr/local/poudriere/data/packages/kn15-kn}

say() { echo "== $*"; }
# IP_VERSION=4: the control machine's IPv6 is a slow tunnel and the builder inherits it; a ports fetch stalled on it (2026-10-14)
PKG="pkg --rootdir $MNT -o ASSUME_ALWAYS_YES=yes -o IGNORE_OSVERSION=yes -o IP_VERSION=4 -o REPOS_DIR=/etc/pkg,/usr/local/etc/pkg/repos"

say "partitions on $DISK"
zpool export $POOL 2>/dev/null || true
gpart destroy -F "$DISK" 2>/dev/null || true
gpart create -s gpt "$DISK"
gpart add -t efi -s 260m -l knefi "$DISK"
gpart add -t freebsd-swap -s 2g -l knswap "$DISK"
gpart add -t freebsd-zfs -l knsys "$DISK"
newfs_msdos -F 32 -c 1 -L KNEFI "/dev/gpt/knefi" >/dev/null

say "pool $POOL, boot environment $VER"
mkdir -p $MNT
zpool create -f -o ashift=12 -o altroot=$MNT -o cachefile=none \
    -O compression=zstd -O atime=off -O mountpoint=none $POOL gpt/knsys
zfs create -o mountpoint=none $POOL/ROOT
zfs create -o mountpoint=/ -o canmount=noauto "$POOL/ROOT/$VER"
zfs mount "$POOL/ROOT/$VER"
# Outside the boot environments: what must stay when the version changes.
zfs create -o mountpoint=/var/db/keelnas $POOL/state
zfs create -o mountpoint=/var/log $POOL/log
zpool set bootfs="$POOL/ROOT/$VER" $POOL

say "base system"
mkdir -p $MNT/usr/share/keys
cp -R /usr/share/keys/pkg* $MNT/usr/share/keys/
$PKG install -r FreeBSD-base $BASE
say "base: $($PKG query '%sb' | awk '{s+=$1} END {printf "%d packages, %d MiB", NR, s/1048576}')"
say "packages from $PKGREPO"
# Only our repository in REPOS_DIR: nothing comes from FreeBSD-ports.
R=$(mktemp -d)
cp "$FILES/keelnas-pkg.pub" $R/keelnas-pkg.pub
cat > $R/keelnas.conf <<EOF
keelnas: { url: "file://$PKGREPO", signature_type: "pubkey", pubkey: "$R/keelnas-pkg.pub", enabled: yes }
EOF
pkg --rootdir $MNT -o ASSUME_ALWAYS_YES=yes -o IGNORE_OSVERSION=yes -o REPOS_DIR=$R install -r keelnas $PORTS
rm -rf $R
say "all: $($PKG query '%sb' | awk '{s+=$1} END {printf "%d packages, %d MiB", NR, s/1048576}')"
$PKG clean -a >/dev/null 2>&1 || true
rm -rf $MNT/var/cache/pkg/* $MNT/var/db/pkg/repos
# licence notices (keelOS docs/45 C): the packages of this image with their licences, the written offer
# of source code; the packages' own licence files in /usr/local/share/licenses stay. The list is also
# printed for the log (tools/licenses/pkglist-from-log.sh), the source bundle is made from it.
DOCD=$MNT/usr/local/share/doc/keelnas
mkdir -p $DOCD
$PKG query '%n\t%v\t%o\t%L\t%l' | sort > $DOCD/packages.txt
cat > $DOCD/README.txt <<'EOT'
keelNAS: third-party software and source code
==============================================
This image is FreeBSD (base packages) plus FreeBSD ports packages, each under its own licence:
packages.txt lists every package with its version, port and licence; the licence files are in
/usr/local/share/licenses. Some are under the GNU GPL, LGPL or the Mozilla Public License.
Samba and smartmontools come from our own package repository, built with poudriere from the FreeBSD ports branch 2026Q4 with our
options (the options and make.conf are in the source bundle).
The complete corresponding source code of those packages, exactly as built (the ports' Makefiles and
patches, the upstream source files, our build options), is published for every version at
https://keelos.dev/sources/ as keelnas-<version>-sources.tar.xz, and kept for at least three years
after we last distribute that version (GPLv2 section 3 equivalent access; GPLv3 section 6; LGPL).
If you cannot download it, write to hello@keelos.dev with the version and we will send it on a
physical medium for the cost of doing so.
EOT
echo PACKAGES-BEGIN; cat $DOCD/packages.txt; echo PACKAGES-END

say "shared libraries"
# A ports package may need a base library that no base package above brought in.
# ldd needs the run-time linker's hints, which rc.d/ldconfig makes at boot: make them here, for the check only.
# Samba built with its own talloc/tdb/tevent (BUNDLED_*, docs/05 section 11) keeps them in
# samba4/private; a few of its libraries and ldb modules have no RUNPATH to there and find them
# at run time because the program that loads them already has them loaded. ldd on such a library
# alone does not know that: give the check the private directory too.
chroot $MNT /bin/sh -c '/sbin/ldconfig -elf /lib /usr/lib /usr/local/lib /usr/local/lib/samba4/private $(cat /usr/local/libdata/ldconfig/* 2>/dev/null)'
MISSING=$(chroot $MNT /bin/sh -c 'find /usr/local/bin /usr/local/sbin /usr/local/lib /usr/local/libexec /usr/bin /usr/sbin /bin /sbin -type f \( -perm -u+x -o -name "*.so*" \) 2>/dev/null | xargs ldd 2>/dev/null' | grep "not found" | sed 's/ (.*//' | sort -u || true)
rm -f $MNT/var/run/ld-elf.so.hints
if [ -n "$MISSING" ]; then
    echo "missing shared libraries:"; echo "$MISSING"
    zpool export $POOL
    exit 1
fi

say "configuration"
echo "$VER" > $MNT/etc/keelnas-version
# No console= line: the loader's own default (console "efi") is right on bhyve. bhyve's UEFI has
# the serial port in ConOut, so "efi" already writes to COM1 and the loader tells the kernel to use
# ttyu0 (RB_SERIAL from ConOut, hw.uart.console from SPCR); "efi,comconsole" wrote every loader
# character twice on COM1 (keelOS docs/09 B24: the loader menu came out as "BBoooott").
cat > $MNT/boot/loader.conf <<EOF
zfs_load="YES"
autoboot_delay="2"
boot_multicons="YES"
EOF
cat > $MNT/etc/rc.conf <<EOF
hostname="keelnas"
ifconfig_vtnet0="DHCP"   # the LAN NIC; vtnet1 is the internal link to keel, set by keelnasd (docs/04 section 11)
background_dhclient="YES"   # the boot does not wait for the LAN's DHCP: keel's internal link comes first
zfs_enable="YES"
sshd_enable="YES"
sendmail_enable="NONE"
syslogd_flags="-ss"
dumpdev="NO"
clear_tmp_enable="YES"
EOF
# No FreeBSD-periodic in the image (docs/05 section 11): its crontab lines would fail every day.
sed -i '' '/[[:space:]]periodic[[:space:]]/d' $MNT/etc/crontab
cat > $MNT/etc/fstab <<EOF
/dev/gpt/knswap  none  swap  sw  0  0
tmpfs            /tmp  tmpfs rw,mode=1777  0  0
EOF
install -m 555 "$FILES/rc.d-keelnas" $MNT/etc/rc.d/keelnas
# keelnasd (built by keelnasd/build.sh, put next to this script as ./files/keelnasd). Without it
# the image has no management daemon and rc.d/keelnas only limits the ZFS cache.
if [ -f "$FILES/keelnasd" ]; then
    install -m 555 "$FILES/keelnasd" $MNT/usr/local/sbin/keelnasd
    ln -sf keelnasd $MNT/usr/local/sbin/keelnas
    say "keelnasd: $($MNT/usr/local/sbin/keelnasd version 2>/dev/null || echo installed)"
else
    say "no ./files/keelnasd: image without keelnasd"
fi
if [ -n "$KEYS" ]; then
    mkdir -p $MNT/root/.ssh
    cp "$KEYS" $MNT/root/.ssh/authorized_keys
    chmod 700 $MNT/root/.ssh; chmod 600 $MNT/root/.ssh/authorized_keys
    echo "PermitRootLogin yes" >> $MNT/etc/ssh/sshd_config
    echo keel | pw -R $MNT usermod root -h 0
fi

say "boot loader"
mount -t msdosfs /dev/gpt/knefi /media
mkdir -p /media/EFI/BOOT
cp $MNT/boot/loader.efi /media/EFI/BOOT/BOOTX64.EFI
umount /media

say "result"
du -sh $MNT 2>/dev/null | awk '{print "files: " $1}'
zfs list -o name,used,refer -r $POOL
zpool export $POOL
say "done: $VER on $DISK"
