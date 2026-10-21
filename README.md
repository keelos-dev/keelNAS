# keelNAS

keelNAS is a small NAS appliance that runs as a virtual machine on [keelOS](https://keelos.dev).
It does two things: it shares files (SMB) and block devices (iSCSI), and it keeps them safe
(protected storage on ZFS, checksums and self-healing, snapshots, disk health). It runs no
applications and has no web page of its own: on keelOS it is managed from the keelNAS tab of
the host's web page, with the host's login.

Status: early. The system image boots, the management program `keelnasd` and its command line
`keelnas` work; expect changes.

## What is here

| Path | What |
|---|---|
| `keelnasd/` | the management program (Go, standard library only): one settings file, from which it writes the Samba and iSCSI configuration and makes the users; an HTTP API; the `keelnas` command line |
| `tools/image/` | builds the system disk from FreeBSD packages: a ZFS root with one boot environment per version |

## The system

- FreeBSD 15.x, installed on a 20 GB virtual disk; ZFS root, one boot environment per version, so
  an upgrade is tried once and falls back by itself.
- Samba for SMB, FreeBSD's ctld for iSCSI, smartmontools.
- `keelnasd` owns the only settings file; `smb4.conf`, `ctl.conf` and the system users are
  generated from it at every start, and a copy of the settings lives on every data pool.

## Releases

Each version is a signed package `keelnas-VERSION.kimg` under [Releases](../../releases). keelOS
lists the newest one in its Market and downloads it from there; a package can also be uploaded to a
keelOS host by hand. keelOS takes only packages signed with a key it trusts, so a package you build
yourself needs your own key in the host's `trusted_keys`.

## Building

`keelnasd/build.sh` builds the program (any machine with Go). `tools/image/build.sh DISK VERSION`
makes the system disk; it runs as root on FreeBSD 15.x and installs FreeBSD's base packages and
the Samba and smartmontools packages onto DISK.

## Using it without keelOS

keelNAS is an ordinary FreeBSD virtual machine and starts on other hypervisors too. There it is
managed with `keelnas` on its console; the web page, the one-step set-up and the backup of the
host's virtual machines exist only on keelOS.

## License

BSD 2-Clause, see [LICENSE](LICENSE). Samba and smartmontools are separate programs under their own licenses
(GPL); the image takes them unmodified from FreeBSD's package repository.
