# keelNAS package set

keelNAS installs Samba and smartmontools from its own package repository, built with poudriere
from a pinned ports branch with our options (docs/05 section 11), not from FreeBSD's ports
packages: the official samba422 package pulls in python, avahi/dbus/X11 and gamin.

| | |
|---|---|
| jail | `kn15`, FreeBSD 15.1-RELEASE (same major version and ABI as the pkgbase 15 the image uses) |
| ports tree | `kn`, git branch `2026Q4` |
| ports | `net/samba422`, `sysutils/smartmontools` and what they need |
| options | `options/net_samba422/options`; `make.conf` (no DOCS, MANPAGES for every port) |
| result | `/usr/local/poudriere/data/packages/kn15-kn/`, signed (RSA) |

Rebuild, as root on a FreeBSD 15 build machine, with this directory copied to it:

    sh build.sh /root/pkgrepo-rsa.key [ZPOOL]

It installs poudriere and git if missing, writes `poudriere.conf`, the make.conf and the options
under `/usr/local/etc/poudriere.d/`, makes the jail and the ports tree once (later runs update the
tree on its branch), checks the samba422 options (and their conflicts) before building, and runs
`poudriere bulk`. A first build takes about 40 minutes with 4 builders. The private key signs the
repository; its public key is `tools/image/files/keelnas-pkg.pub`, which `tools/image/build.sh`
trusts. Keep the private key out of every repository and remove the copy from the build
machine afterwards. ZPOOL (optional) is a ZFS pool for poudriere's datasets; without it
poudriere runs with NO_ZFS.

To move to the next quarterly branch: change BRANCH in build.sh, remove the old tree
(`poudriere ports -d -p kn`), rebuild, and test the image.
