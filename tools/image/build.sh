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
# packages (pkgbase) and the ports packages, nothing is compiled here.
# With AUTHORIZED_KEYS it is a test build: root logs in over ssh with those keys and with the
# password "keel" on the console.
set -eu

DISK=$1
VER=$2
KEYS=${3:-}
POOL=knsys
MNT=/mnt/kn
FILES=$(dirname "$0")/files   # copied next to this script
# FreeBSD-libexecinfo: Samba's libraries need libexecinfo.so, which the minimal set does not have.
BASE="FreeBSD-set-minimal FreeBSD-kernel-generic FreeBSD-bootloader FreeBSD-zfs FreeBSD-ctl FreeBSD-ssh FreeBSD-libexecinfo"
PORTS="samba422 smartmontools"

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
say "ports packages"
$PKG install -r FreeBSD-ports $PORTS
$PKG clean -a >/dev/null 2>&1 || true
rm -rf $MNT/var/cache/pkg/* $MNT/var/db/pkg/repos

say "shared libraries"
# A ports package may need a base library that no base package above brought in.
# ldd needs the run-time linker's hints, which rc.d/ldconfig makes at boot: make them here, for the check only.
chroot $MNT /bin/sh -c '/sbin/ldconfig -elf /lib /usr/lib /usr/local/lib $(cat /usr/local/libdata/ldconfig/* 2>/dev/null)'
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
