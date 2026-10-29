#!/bin/sh
# Build the keelNAS package set (docs/05 section 11) with poudriere. Runs as root on FreeBSD 15.x,
# idempotent: a second run updates the ports tree on its branch and rebuilds what changed.
#
#   build.sh SIGNING_KEY [ZPOOL]
#
# SIGNING_KEY is the RSA private key that signs the repository (its public key is
# tools/image/files/keelnas-pkg.pub). ZPOOL is a ZFS pool for poudriere (default: none, then
# poudriere runs on UFS with NO_ZFS). The repository ends up in
#   /usr/local/poudriere/data/packages/kn15-kn/
# which tools/image/build.sh installs Samba and smartmontools from.
set -eu

KEY=$1
ZPOOL=${2:-}
JAIL=kn15
REL=15.1-RELEASE
TREE=kn
BRANCH=2026Q4
PORTS="net/samba422 sysutils/smartmontools"
HERE=$(cd "$(dirname "$0")" && pwd)
ETC=/usr/local/etc
PD=$ETC/poudriere.d

say() { echo "== $*"; }
[ -f "$KEY" ] || { echo "no signing key $KEY" >&2; exit 1; }
export IP_VERSION=4
command -v poudriere >/dev/null && command -v git >/dev/null || pkg install -y poudriere git-lite

say "configuration"
if [ -n "$ZPOOL" ]; then Z="ZPOOL=$ZPOOL\nZROOTFS=/poudriere"; else Z="NO_ZFS=yes"; fi
awk -v z="$Z" -v k="$KEY" '/^@ZFS@$/ {gsub(/\\n/, "\n", z); print z; next} {sub(/@KEY@/, k); print}' \
    "$HERE/poudriere.conf" > $ETC/poudriere.conf
mkdir -p $PD/$JAIL-$TREE-options /usr/local/poudriere/distfiles
cp "$HERE/make.conf" $PD/$JAIL-$TREE-make.conf
rm -rf $PD/$JAIL-$TREE-options/*
cp -R "$HERE/options/" $PD/$JAIL-$TREE-options/

say "jail $JAIL ($REL)"
poudriere jail -l -n -q | grep -qx $JAIL || poudriere jail -c -j $JAIL -v $REL -m http
say "ports tree $TREE ($BRANCH)"
if poudriere ports -l -n -q | grep -qx $TREE; then
    poudriere ports -u -p $TREE
else
    poudriere ports -c -p $TREE -m git+https -B $BRANCH
fi
PT=$(poudriere ports -l -q | awk -v t=$TREE '$1 == t {print $NF}')
COMMIT=$(git -C "$PT" rev-parse --short=12 HEAD)
echo "ports $BRANCH at $COMMIT"

say "options check"
# The options as the port sees them with our files: fail before a long build if one did not take.
O=$(make -C "$PT/net/samba422" PORTSDIR="$PT" PORT_DBDIR=$PD/$JAIL-$TREE-options __MAKE_CONF=$PD/$JAIL-$TREE-make.conf -V SELECTED_OPTIONS)
echo "samba422: $O"
for o in AD_DC PYTHON3 AVAHI FAM CUPS GPGME DOCS MANPAGES; do
    case " $O " in *" $o "*) echo "option $o is still on" >&2; exit 1 ;; esac
done
for o in MDNSRESPONDER BUNDLED_TALLOC BUNDLED_TDB BUNDLED_TEVENT ADS LDAP FRUIT; do
    case " $O " in *" $o "*) ;; *) echo "option $o is off" >&2; exit 1 ;; esac
done

# conflicts between options (the port's own check-sanity, which -V does not run)
make -C "$PT/net/samba422" PORTSDIR="$PT" PORT_DBDIR=$PD/$JAIL-$TREE-options __MAKE_CONF=$PD/$JAIL-$TREE-make.conf check-config

say "bulk"
poudriere bulk -j $JAIL -p $TREE $PORTS
echo "$COMMIT" > /usr/local/poudriere/data/packages/$JAIL-$TREE/.ports-commit
say "done: /usr/local/poudriere/data/packages/$JAIL-$TREE (ports $COMMIT)"
