#!/bin/sh
# Tiered raidz feasibility. Scale: 1T = 100M. Disks: A=4T B=4T C=8T D=8T, tiers of 4T.
# Run as root on FreeBSD 15 (OpenZFS 2.3 or later) with ~5 GB free in /var/tmp. Result: docs/02-survey.md section 6.
Z=/var/tmp/z; rm -rf $Z; mkdir -p $Z; cd $Z
zpool destroy t1 2>/dev/null; zpool destroy t2 2>/dev/null
for f in a1 b1 c1 c2 d1 d2 e1 e2 s1 s2 fake; do truncate -s 400M $f; done
say() { echo; echo "=== $*"; }
cap() { zfs list -H -p -o used,avail $1 | awk '{printf "used %.0fM avail %.0fM total %.0fM\n",$1/1048576,$2/1048576,($1+$2)/1048576}'; }
say "E1 two-member raidz1 (A1,B1)"
zpool create -o ashift=12 -O compression=off t1 raidz1 $Z/a1 $Z/b1 && cap t1
dd if=/dev/random of=/t1/f1 bs=1M count=200 2>/dev/null; S1=$(sha256 -q /t1/f1); cap t1
say "E2 add disk C: expand tier 1 with C1 (2 -> 3 members)"
zpool attach -w t1 raidz1-0 $Z/c1; echo "rc=$?"; zpool status t1 | grep -i "expand\|raidz1-0\|state:" ; cap t1
dd if=/dev/random of=/t1/f2 bs=1M count=300 2>/dev/null; S2=$(sha256 -q /t1/f2); cap t1
say "E3 add disk D: expand tier 1 with D1 (3 -> 4), new tier 2 = raidz1(C2,D2)"
zpool attach -w t1 raidz1-0 $Z/d1; echo "rc=$?"; cap t1
zpool add t1 raidz1 $Z/c2 $Z/d2; echo "add rc=$?"
zpool list -v t1 | cut -c1-90; cap t1
dd if=/dev/random of=/t1/f3 bs=1M count=500 2>/dev/null; S3=$(sha256 -q /t1/f3); cap t1
say "E4 zfs rewrite (old blocks to the new data:parity ratio)"
zfs rewrite -r /t1 2>&1 | tail -2; echo "rewrite rc=$?"; zpool sync t1; cap t1
say "E5 disk C dies (C1 and C2 gone), read everything, then replace by E"
zpool offline t1 $Z/c1; zpool offline t1 $Z/c2; zpool status t1 | grep "state:"
[ "$(sha256 -q /t1/f1)" = "$S1" ] && [ "$(sha256 -q /t1/f2)" = "$S2" ] && [ "$(sha256 -q /t1/f3)" = "$S3" ] && echo "degraded read: sums OK"
zpool replace -w t1 $Z/c1 $Z/e1; echo "rc=$?"; zpool replace -w t1 $Z/c2 $Z/e2; echo "rc=$?"
zpool scrub -w t1; zpool status t1 | grep "state:\|scan:\|errors:"
zpool export t1; zpool import -d $Z t1
[ "$(sha256 -q /t1/f1)" = "$S1" ] && [ "$(sha256 -q /t1/f2)" = "$S2" ] && [ "$(sha256 -q /t1/f3)" = "$S3" ] && echo "after replace + reimport: sums OK"
say "E6 one-disk start: raidz1 made with a missing member, completed later"
zpool create -o ashift=12 -O compression=off t2 raidz1 $Z/s1 $Z/fake; zpool offline t2 $Z/fake; rm $Z/fake
zpool status t2 | grep "state:"; dd if=/dev/random of=/t2/g1 bs=1M count=150 2>/dev/null; G1=$(sha256 -q /t2/g1); cap t2
zpool replace -w t2 $Z/fake $Z/s2; echo "rc=$?"; zpool scrub -w t2; zpool status t2 | grep "state:\|errors:"
[ "$(sha256 -q /t2/g1)" = "$G1" ] && echo "sums OK"
say "E7 expansion while a tier is degraded"
truncate -s 400M $Z/s3; zpool offline t2 $Z/s2; zpool attach -w t2 raidz1-0 $Z/s3; echo "attach-while-degraded rc=$?"
zpool online t2 $Z/s2; zpool status t2 | grep "state:"
zfs version | head -1
