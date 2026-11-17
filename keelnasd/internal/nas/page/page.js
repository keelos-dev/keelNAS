// keelNAS's page (docs/04 section 16): the contents of the keelNAS tab in keelOS's web. keelnasd
// serves this file (GET /v1/page, signed at build time); keel checks the signature and loads it
// as a module at v1/nas/<id>/page.js, so a keelNAS upgrade brings its own page and keel needs
// none. keel keeps the frame: the instance list, making, renaming, deleting and upgrading an
// instance, and what to show while it cannot be reached.
//
// The contract with keel (pageAPI below; raise it when this file needs more of keel):
//   ../../../app/api.js   api(method, path[, body]) (errors carry .status), html (htm bound to Preact)
//   ../../../app/i18n.js  t (keel's own words, the fallback), getLang
//   ../../../app/time.js  fmtTime
//   ../../../app/ui.js    ask, askText, Bar, Dialog, FormDialog, gib, Info, Spinner, toast
//   preact/hooks   through keel's import map
// and keel's style sheet. This file exports pageAPI, tabs, tabLabel(k) and the default
// component Page({ base, tab, here, admin, info, st, tick, bump }): base = the API of this
// instance (/v1/nas/<id>, forwarded to keelnasd), here = the hash prefix of its sub-tabs, info =
// keel's record of the instance, st = GET <base>/status, bump() = read everything again.
import { useEffect, useState } from "preact/hooks";
import { api, html } from "../../../app/api.js";
import { getLang, t as keelT } from "../../../app/i18n.js";
import { fmtTime } from "../../../app/time.js";
import { ask, askText, Bar, Dialog, FormDialog, gib, Info, Spinner, toast } from "../../../app/ui.js";

export const pageAPI = 1;

// The page's own words; a key not here is asked of keel.
const words = {
 "en": {
  "nas.make.market": "Get keelNAS in the market",
  "nas.what": "keelNAS is a small NAS in a virtual machine of this host: shared folders (SMB) and iSCSI, with snapshots and protected storage. It is managed here.",
  "nas.make.title": "Deploy a keelNAS",
  "nas.make.go": "Deploy and start",
  "nas.make.image": "keelNAS version",
  "nas.make.net": "Network",
  "nas.make.ram": "Memory (GiB)",
  "nas.make.data": "Data disk on this host's pool (GiB, 0: none)",
  "nas.make.note": "A 20 GiB system disk is made from the package. Disks and controllers of the host can be given to the guest afterwards, on its page.",
  "nas.waiting": "keelNAS is starting. It appears here by itself, usually within a minute.",
  "nas.thevm": "The virtual machine",
  "nas.forget": "Forget this keelNAS",
  "nas.forgetq": "Forget this keelNAS? The virtual machine and its data stay; this page no longer manages it.",
  "nas.edit": "Edit",
  "nas.editshare": "Who may use {0}",
  "nas.passwd": "Password",
  "nas.passwdof": "A new password for {0}",
  "nas.protectbtn": "Protect",
  "nas.protecttitle": "Protect {0}",
  "nas.protectnote": "The disk becomes the mirror of this storage. Everything on it is erased.",
  "scrub.every.auto": "Every 30 days",
  "nas.scrub.auto": "Every 30 days after the last check, at {0}",
  "nas.scrub.utc": "UTC",
  "nas.scrub.note": "A check reads all data on the storage and repairs what a mirror can repair. Shares stay available; they are slower while it runs. \"Every 30 days\" checks 30 days after the last check. Times are UTC: keelNAS has no time zone of its own.",
  "nas.scrub.nobytes": "took {0} (not watched: no speed)",
  "nas.scrub.left": "{0} left",
  "nas.scrub.lastnone": "Not checked yet.",
  "nas.scrub.superseded": "another scan took its place before its end was seen",
  "nas.dev.disk": "Disk",
  "nas.dev.state": "State",
  "nas.dev.size": "Size",
  "nas.attach.title": "Add a disk to {0}",
  "nas.attach.hint": "The disk becomes one more copy of the data (a mirror); the storage copies everything onto it, which shows here as a resilver. Everything on the disk is erased. It must be at least as large as the disks it mirrors.",
  "nas.detachq": "Take {0} out of {1}? The data stays on the other disk(s); with one disk left the storage has no second copy until a disk is added again.",
  "nas.replace": "Replace",
  "nas.replace.title": "Replace {0}",
  "nas.replace.hint": "The new disk takes the place of {0}: the storage copies the data onto it (a resilver) and then lets the old disk go. Everything on the new disk is erased; it must be at least as large.",
  "nas.replace.missing": "{0} (missing, was {1})",
  "nas.export": "Export",
  "nas.export.title": "Export {0}",
  "nas.export.note": "The storage leaves keelNAS with its data, for example to move its disks to another machine. Its shares and LUNs must be removed first. It can be imported again from \"Pools found on the disks\".",
  "nas.delete": "Delete",
  "nas.delete.title": "Delete {0}",
  "nas.delete.note": "Everything on this storage is destroyed and its disks are free again. This cannot be undone. Its shares and LUNs must be removed first.",
  "nas.confirmname": "Type the storage's name ({0}) to confirm",
  "nas.expand.note": "The storage can grow by {0}: its disks are bigger than it uses (a bigger disk replaced a smaller one).",
  "nas.expandq": "Grow {0} to the full size of its disks (zpool online -e)? Nothing is erased; it cannot be shrunk again.",
  "nas.expanded": "{0} is {1} now",
  "nas.labelclear": "Clear labels",
  "nas.labelclear.title": "Clear the ZFS labels on {0}",
  "nas.labelclear.note": "Disk {0} carries the label of pool {1}, which is not imported. Clearing it destroys what that pool had on this disk; afterwards the disk is free for storage.",
  "nas.labelclear.confirm": "Type {0} to confirm",
  "nas.devhelp": "Add: one more copy of the data (a single disk becomes a mirror). Detach: a disk leaves its mirror (never the last working one). Replace: a new disk takes the place of a missing, failing or smaller one.",
  "nas.pickdisks": "One disk, or two that hold the same.",
  "nas.keepnote": "0 = none of that kind. Snapshots taken by hand are never removed by themselves.",
  "nas.newuser": "New user",
  "nas.newlun": "New LUN",
  "nas.noshares": "No share yet.",
  "nas.noluns": "No LUN yet.",
  "nas.tab.overview": "Overview",
  "nas.tab.storage": "Storage",
  "nas.tab.shares": "Shares",
  "nas.tab.users": "Users",
  "nas.tab.iscsi": "iSCSI",
  "nas.alerts": "Needs attention",
  "nas.alert.pool_degraded": "{0} has a disk that is not working: the data is no longer protected. Replace the disk.",
  "nas.alert.pool_no_protection": "{0} is a single disk: if it fails, its data is gone. Add a second disk to protect it.",
  "nas.alert.pool_full": "{0} is almost full.",
  "nas.alert.disk_failing": "Disk {0} reports that it is failing. Replace it.",
  "nas.alert.scrub_errors": "The last check of {0} found errors that could not be repaired.",
  "nas.alert.apply_failed": "keelNAS could not apply its settings: {0}",
  "nas.alert.snapshot_failed": "A snapshot of {0} failed.",
  "nas.snaps": "Snapshots",
  "nas.snapsof": "Snapshots of {0}",
  "nas.snapnow": "Take one now",
  "nas.nosnaps": "No snapshot yet: one is taken every hour when something changed.",
  "nas.snaptime": "Time",
  "nas.snapclass": "Kind",
  "nas.class.hourly": "hourly",
  "nas.class.daily": "daily",
  "nas.class.weekly": "weekly",
  "nas.class.manual": "by hand",
  "nas.delsnapq": "Delete the snapshot of {0}?",
  "nas.snapnote": "In Windows: right-click a file or folder of the share, Properties, Previous Versions.",
  "nas.keep": "Automatic snapshot settings",
  "nas.keep.hourly": "Hourly",
  "nas.keep.daily": "Daily",
  "nas.keep.weekly": "Weekly",
  "nas.save": "Save",
  "nas.saved": "Saved",
  "nas.smartbad": "failing",
  "nas.none": "This host has no keelNAS.",
  "nas.down": "keelNAS does not answer.",
  "nas.retry": "Try again",
  "nas.healthy": "healthy",
  "nas.storage": "Storage",
  "nas.storagen": "Storage {0}",
  "nas.nostorage": "No storage yet.",
  "nas.nodisks": "No free disk: give the keelNAS guest a disk first.",
  "nas.disk.busy": "Disks keelNAS does not use",
  "nas.disk.foreign": "belongs to pool {0}, which is not imported: keelNAS never writes on it (import the pool to use its data)",
  "nas.disk.destroyed": "holds the destroyed pool {0}: its data could still be recovered, so keelNAS does not write on it",
  "nas.disk.inpool": "in pool {0}",
  "nas.disk.parts": "not empty: {0} partitions",
  "nas.disk.fs": "not empty: {0} file system",
  "nas.disk.data": "not empty: there is data at its start",
  "nas.disk.unknown": "could not be checked: not used",
  "nas.imp.found": "Pools found on the disks",
  "nas.imp.what": "These ZFS pools were made elsewhere and are not imported. keelNAS does not touch their disks; import one to use it, with its data.",
  "nas.imp.disks": "{0} disks",
  "nas.imp.raw": "raw",
  "nas.imp.lastby": "last used by another system ({0})",
  "nas.imp.btn": "Import",
  "nas.imp.go": "Import the pool",
  "nas.imp.title": "Import pool {0}",
  "nas.imp.note": "The pool and its data stay as they are (no upgrade, no setting changed). Its file systems are mounted below {0} inside keelNAS, so their own mount points cannot cover the system. keelNAS keeps a copy of its settings in the pool, as in every storage.",
  "nas.imp.taken": "keelNAS already has a pool named {0}: import this one under another name (the new name is written into the pool).",
  "nas.imp.name": "Name",
  "nas.imp.otherhost": "This pool was last used by another system ({0}). That system no longer uses these disks: take the pool over.",
  "nas.protected": "protected",
  "nas.unprotected": "not protected",
  "nas.onhost": "on the host's storage",
  "nas.addprotect": "Protect with {0} ({1})",
  "nas.protectq": "Add {0} to {1} as its mirror? Everything on {0} is erased.",
  "nas.newstorage": "New storage",
  "nas.mirror": "Protect the data: the two disks hold the same (recommended)",
  "nas.twoneedmirror": "Two disks without protection are not offered: make two storages instead.",
  "nas.make": "Make the storage",
  "nas.users": "Users",
  "nas.usersnote": "Who may open the shares. These are not logins to keelOS.",
  "nas.username": "User name",
  "nas.password": "Password",
  "nas.adduser": "Add the user",
  "nas.delete": "Delete",
  "nas.deluserq": "Delete the user {0}? It is taken out of every share.",
  "nas.shares": "Shares",
  "nas.share": "Share",
  "nas.address": "Address",
  "nas.rw": "Read and write",
  "nas.ro": "Read only",
  "nas.noaccess": "No access",
  "nas.manager": "Manager",
  "nas.managernote": "A manager has full control of everything in the share, in every folder whatever its list says, and may change permissions in Windows (Properties, Security).",
  "nas.rootmanagers": "Only managers make, rename or delete folders in the share's root",
  "nas.folders": "Folders",
  "nas.foldersof": "Folders of {0}",
  "nas.folder": "Folder",
  "nas.fd.who": "Who may use it",
  "nas.fd.likeshare": "like the share",
  "nas.fd.plus": "like the share, and {0}",
  "nas.fd.mgronly": "managers only",
  "nas.fd.other": "more set in Windows",
  "nas.fd.new": "New folder",
  "nas.fd.make": "Make the folder",
  "nas.fd.namehint": "Any name Windows takes: not \\ / : * ? \" < > |, no space or dot at the end.",
  "nas.fd.mode.share": "Like the share: everybody who may use the share",
  "nas.fd.mode.own": "Only these (and the share's managers)",
  "nas.fd.note": "A folder with a list of its own lets in only who is named; everybody else does not even see it. What is inside follows the folder. These are the folder's real permissions: Windows shows the same ones under Properties, Security, and what is changed there shows here.",
  "nas.fd.othernote": "This folder has permissions set in Windows that are neither \"read and write\" nor \"read only\". Saving here replaces them.",
  "nas.fd.again": "Apply again",
  "nas.fd.againnote": "A file moved here from another folder keeps the permissions it had. \"Apply again\" gives every file the permissions of its folder.",
  "nas.fd.delq": "Delete the folder {0}? Only an empty folder is deleted.",
  "nas.fd.none": "No folder in this share yet.",
  "nas.fd.oldkeel": "Folders need a newer keelOS on this host.",
  "nas.acl.jobcount": "setting permissions: counting files…",
  "nas.acl.jobrun": "setting permissions: {0} of {1}",
  "nas.acl.jobfailed": "setting permissions failed: {0}",
  "nas.used": "Used",
  "nas.newshare": "New share",
  "nas.sharename": "Name",
  "nas.addshare": "Make the share",
  "nas.sharenote": "Every share needs a user and a password: Windows refuses shares that are open to everybody.",
  "nas.sharesneed": "A share needs a storage and a user first.",
  "nas.delshareq": "Remove the share {0}? Its files stay on the storage.",
  "nas.size": "Size",
  "nas.lunname": "Name",
  "nas.namehint": "lower-case letters, digits, - and _; starts with a letter; up to 32 characters (e.g. lun0, backup-1)",
  "nas.addlun": "Make the LUN",
  "nas.dellunq": "Remove the LUN {0}? Its data stays on the storage.",
  "nas.mem": "memory {0} free of {1}, cache {2}",
  "nas.dir.title": "Accounts from keelAuth",
  "nas.dir.what": "Connect this keelNAS to a keelAuth: its users and groups can then open the shares with their keelAuth password. Password changes, disabled and deleted accounts follow within seconds. The local users stay as they are.",
  "nas.dir.none": "There is no keelAuth on this host yet.",
  "nas.dir.toaccounts": "Accounts",
  "nas.dir.connect": "Connect to keelAuth",
  "nas.dir.connectgo": "Connect",
  "nas.dir.from": "Users and groups from {0}",
  "nas.dir.synced": "last checked {0}",
  "nas.dir.syncnow": "Sync now",
  "nas.dir.disconnect": "Disconnect",
  "nas.dir.disconnectq": "Disconnect from {0}",
  "nas.dir.whatbecomes": "What becomes of keelAuth's users on this keelNAS?",
  "nas.dir.keep": "Keep them as local users (those who can log in keep their password; a share's groups become their members)",
  "nas.dir.delete": "Delete them (they lose access to every share)",
  "nas.dir.nousers": "keelAuth has no users yet.",
  "nas.dir.display": "Name",
  "nas.dir.groups": "Groups",
  "nas.dir.state": "SMB",
  "nas.dir.ok": "can log in",
  "nas.dir.disabled": "disabled in keelAuth",
  "nas.dir.nopw": "no SMB password yet: set the password once more in keelAuth",
  "nas.dir.p.local_user": "not made: a local user has this name",
  "nas.dir.p.system_account": "not made: a system account has this name",
  "nas.dir.p.bad_uid": "not made: its user number cannot be used",
  "nas.dir.p.bad_name": "not made: the name cannot be used",
  "nas.dir.p.bad_gid": "not made: its group number cannot be used",
  "nas.dir.p.system_group": "not made: a system group has this name",
  "nas.dir.note": "Users and groups are changed in keelAuth (the Accounts tab), not here. Shares can name them; a group is written @name.",
  "nas.dir.userlabel": "{0} (keelAuth)",
  "nas.dir.grouplabel": "Group {0} (keelAuth)",
  "nas.ds.dataset": "Dataset",
  "nas.ds.auto": "Auto snapshots",
  "nas.ds.autohint": "Hourly, daily and weekly snapshots of this dataset (how many are kept: the button below the list). A shared dataset has them until switched off, any other one once switched on.",
  "nas.ds.autoon": "Auto snapshots of {0} on",
  "nas.ds.autooff": "Auto snapshots of {0} off",
  "nas.ds.notmounted": "(not mounted)",
  "nas.ds.sharedas": "shared as {0}",
  "nas.ds.share": "Share",
  "nas.ds.props": "Share settings",
  "nas.ds.browse": "Browse",
  "nas.ds.new": "New dataset",
  "nas.ds.newin": "New dataset in {0}",
  "nas.ds.make": "Make the dataset",
  "nas.ds.delq": "Delete the dataset {0} with all its files and snapshots? This cannot be undone.",
  "nas.ds.sharetitle": "Share {0}",
  "nas.ds.browsetitle": "Files in {0}",
  "nas.ds.empty": "Empty.",
  "nas.ds.trunc": "Only the first {0} entries are shown.",
  "nas.ds.name": "Name",
  "nas.ds.modified": "Modified",
  "nas.ds.link": "(link)",
  "nas.ds.readonly": "Only a look: files are changed through the share.",
  "nas.ds.ownercol": "Owner",
  "nas.ds.chown": "Change owner",
  "nas.ds.chowntitle": "Change the owner of {0}",
  "nas.ds.chownuser": "New owner",
  "nas.ds.chowngo": "Change the owner",
  "nas.ds.ownernowis": "Its files now belong to {0}.",
  "nas.ds.chownnote": "Every file and folder of this dataset is given to this user and the group keelnas: folders 2770, files 0660, as in a dataset keelNAS makes. The old owners and permissions are not kept. Datasets below it are left as they are. On a big dataset this takes a while; the progress shows here.",
  "nas.ds.needowner": "Its files do not belong to a keelNAS user: change the owner first.",
  "nas.ds.jobcount": "changing the owner: counting files…",
  "nas.ds.jobrun": "changing the owner: {0} of {1}",
  "nas.ds.jobfailed": "owner change failed: {0}",
  "nas.net.old": "This keelNAS is an older version without network settings. Make it again from the market to get them.",
  "nas.make.name": "Name",
  "nas.instances": "keelNAS on this host",
  "nas.ok": "running",
  "nas.alertsn": "{0} to look at",
  "nas.all": "All keelNAS",
  "nas.renameq": "New name:",
  "nas.rename": "Rename",
  "nas.deleteq": "Delete {0}? Its virtual machine and all its disks, the shared files with them, are deleted. This cannot be undone.",
  "nas.deleteinst": "Delete",
  "nas.off": "keelNAS is off: its virtual machine is not running.",
  "nas.start": "Start keelNAS",
  "nas.suspended": "keelNAS is suspended.",
  "nas.resume": "Resume keelNAS",
  "nas.missing": "The virtual machine of this keelNAS is gone (deleted).",
  "nas.tab.network": "Network",
  "nas.tab.backups": "Backup fetching",
  "nas.ar.open": "Search mail",
  "nas.ar.title": "Mail archive in the backups of {0}",
  "nas.ar.what": "keelMail's archive (every message, 30 days each) inside the copies kept here: up to a year back. Searches, views and downloads are recorded.",
  "nas.ar.audit": "Audit log",
  "nas.ar.noaudit": "Nothing yet.",
  "nas.ar.a.search": "search",
  "nas.ar.a.view": "view",
  "nas.ar.a.download": "download",
  "nas.ar.from_d": "From",
  "nas.ar.to_d": "To",
  "nas.ar.dir": "Direction",
  "nas.ar.dir.all": "all",
  "nas.ar.dir.in": "received",
  "nas.ar.dir.out": "sent",
  "nas.ar.dir.internal": "internal",
  "nas.ar.from": "Sender",
  "nas.ar.to": "Recipient",
  "nas.ar.subject": "Subject",
  "nas.ar.att": "Attachment",
  "nas.ar.body": "Text contains",
  "nas.ar.search": "Search",
  "nas.ar.searching": "Searching the copies…",
  "nas.ar.n": "{0} messages",
  "nas.ar.more": "the newest {0} shown: narrow the conditions",
  "nas.ar.missing": "{0} … {1} are not in a backup yet: search them on keelMail.",
  "nas.ar.time": "Time",
  "nas.ar.nosubject": "(no subject)",
  "nas.ar.copy": "From the copy",
  "nas.ar.eml": "Download (.eml)",
  "nas.bk.what": "keelNAS fetches the daily backups of other keel systems (keelMail, keelAuth …) every night with their read-only pull token and keeps them here, encrypted, by its own rules. Whoever breaks into such a system cannot delete them here.",
  "nas.bk.add": "Add a source",
  "nas.bk.addgo": "Check and add",
  "nas.bk.added": "{0} added: the first fetch is at its hour (or “Fetch now”)",
  "nas.bk.token": "Pull token",
  "nas.bk.tokenhint": "Made on the source: keelMail → Backup → Fetched by the backup machine → Make a pull token. keelNAS connects to the source now to check it.",
  "nas.bk.pool": "Storage",
  "nas.bk.hour": "Fetch at",
  "nas.bk.hourhint": "Every day at this hour (UTC: keelNAS has no time zone of its own; 17:00 UTC is 02:00 in Japan). After a failure it tries again every hour.",
  "nas.bk.newtoken": "New pull token (empty: keep)",
  "nas.bk.newtokenhint": "After a new token was made on the source.",
  "nas.bk.keep": "How long to keep",
  "nas.bk.keepd": "Daily copies to keep",
  "nas.bk.keepm": "Monthly copies to keep (the first of each month)",
  "nas.bk.keepnow": "Kept here: the newest {0} daily copies and the first of each of the last {1} months.",
  "nas.bk.source": "Source",
  "nas.bk.state": "State",
  "nas.bk.last": "Last fetched",
  "nas.bk.copies": "Copies",
  "nas.bk.oldest": "oldest {0}",
  "nas.bk.size": "Size",
  "nas.bk.running": "fetching",
  "nas.bk.paused": "paused",
  "nas.bk.failed": "failed",
  "nas.bk.ok": "ok",
  "nas.bk.never": "not yet",
  "nas.bk.now": "Fetch now",
  "nas.bk.fetching": "Fetching: it runs in the background",
  "nas.bk.pause": "Pause",
  "nas.bk.resume": "Resume",
  "nas.bk.delete": "Remove",
  "nas.bk.delq": "Stop fetching {0}?",
  "nas.bk.deldataq": "Also delete the copies kept here ({0})? Cancel keeps them.",
  "nas.bk.none": "No sources yet.",
  "nas.bk.nopool": "Make a storage first: the copies are kept on it.",
  "nas.bk.keyset": "The copies are encrypted with a key that is only on this keelNAS's system disk.",
  "nas.bk.keynone": "The encryption key is made with the first fetch.",
  "nas.bk.showkey": "Show the key",
  "nas.bk.key": "Backup encryption key",
  "nas.bk.keynote": "Keep it somewhere safe (a password manager, on paper). If this keelNAS's system disk is lost, the copies can only be opened with it.",
  "nas.bk.old": "This keelNAS is too old to fetch backups: upgrade it.",
  "nas.tab.hardware": "Hardware",
  "nas.hw.what": "Every disk of this keelNAS with what SMART says now: health, temperature, power-on time, reallocated and pending sectors (wear and media errors on NVMe). The health mark opens everything the disk reports about itself. Read from the disks when the page opens and every minute while it is open; a sleeping disk is left asleep and shows the daily check's values.",
  "nas.hw.old": "This keelNAS is too old for the hardware page: upgrade it (overview, upgrade) to a version from 2026-10-14 or later.",
  "nas.hw.storage": "Storage",
  "nas.hw.virtual": "Virtual disks",
  "nas.hw.ndisks": "{0} disks",
  "nas.hw.system": "system",
  "nas.hw.standby": "standby",
  "nas.hw.standbytip": "the disk was asleep and was left so; these are the daily check's values",
  "nas.hw.readat": "read {0}",
  "nas.net": "LAN",
  "nas.net.mode": "Address from",
  "nas.net.dhcp": "DHCP (automatic)",
  "nas.net.static": "Fixed address",
  "nas.net.addr": "Address",
  "nas.net.gw": "Gateway",
  "nas.net.dns": "DNS servers",
  "nas.net.nic": "Network card",
  "nas.net.link": "connected",
  "nas.net.nolink": "no link",
  "nas.net.internal": "keelOS manages keelNAS over its own internal link ({0}), so a wrong setting here can always be corrected.",
  "nas.net.nointernal": "This keelNAS has no internal link to keelOS (it was made before there was one): keelOS reaches it over the LAN, and a wrong address cuts it off. Make it again from the market to get the link.",
  "nas.net.title": "keelNAS on the LAN",
  "nas.net.note": "The new setting takes effect a few seconds after saving. Computers that use the shares by the old address must use the new one.",
  "nas.net.saved": "Saved; keelNAS takes the new address in a few seconds.",
  "nas.hw.health": "Health",
  "nas.hw.h.tip": "everything this disk reports about itself (SMART, error log)",
  "nas.hw.realloc": "Reallocated/pending",
  "nas.hw.mediaerr": "Media errors",
  "nas.hw.h.mediatip": "the drive's own count of media and data integrity errors (NVMe health log): reads or writes whose data failed its check, counted over the drive's whole life",
  "nas.hw.disks": "Disks",
  "nas.hw.name": "Name",
  "nas.hw.model": "Model",
  "nas.hw.serial": "Serial number",
  "nas.hw.size": "Size",
  "nas.hw.temp": "Temp.",
  "nas.hw.hours": "Power-on",
  "nas.hw.wear": "Life used",
  "nas.hw.passed": "OK",
  "nas.hw.failed": "FAILING",
  "nas.hw.nosmart": "no SMART",
  "nas.hw.h.nvmelog": "NVMe health log (log page 2)",
  "nas.hw.h.title": "{0}: health details"
 },
 "zh": {
  "nas.make.market": "到市场里获取 keelNAS",
  "nas.what": "keelNAS 是这台主机上一台虚拟机里的小 NAS：共享文件夹（SMB）和 iSCSI，带快照和有保护的存储。在这里管理。",
  "nas.make.title": "部署一台 keelNAS",
  "nas.make.go": "部署并启动",
  "nas.make.image": "keelNAS 版本",
  "nas.make.net": "网络",
  "nas.make.ram": "内存（GiB）",
  "nas.make.data": "数据盘，放在主机的池上（GiB，0 = 不要）",
  "nas.make.note": "系统盘 20 GiB，从包里生成。主机上的盘和控制器可以之后在这台虚拟机的页面上交给它。",
  "nas.waiting": "keelNAS 正在启动，好了以后这里会自己出现，一般不到一分钟。",
  "nas.thevm": "这台虚拟机",
  "nas.forget": "忘掉这台 keelNAS",
  "nas.forgetq": "忘掉这台 keelNAS？虚拟机和数据都留着，只是这个页面不再管它。",
  "nas.edit": "编辑",
  "nas.editshare": "谁能用 {0}",
  "nas.passwd": "改密码",
  "nas.passwdof": "{0} 的新密码",
  "nas.protectbtn": "加保护",
  "nas.protecttitle": "给{0}加保护",
  "nas.protectnote": "这块盘会成为这个存储的镜像。盘上原有的内容会被清掉。",
  "scrub.every.auto": "每 30 天",
  "nas.scrub.auto": "上次检查 30 天后，{0}",
  "nas.scrub.utc": "UTC",
  "nas.scrub.note": "检查会把存储上的数据全部读一遍，镜像能修的就修好。共享照常能用，只是检查时慢一些。\"每 30 天\"是上一次检查满 30 天后再查。时间是 UTC：keelNAS 没有自己的时区。",
  "nas.scrub.nobytes": "用了 {0}（没有记录速度）",
  "nas.scrub.left": "还要 {0}",
  "nas.scrub.lastnone": "还没检查过。",
  "nas.scrub.superseded": "还没看到结束，就被下一次扫描接替了",
  "nas.dev.disk": "盘",
  "nas.dev.state": "状态",
  "nas.dev.size": "大小",
  "nas.attach.title": "给 {0} 加一块盘",
  "nas.attach.hint": "这块盘成为数据的又一份副本（镜像）；存储会把数据全部复制上去，这里显示为 resilver。盘上原有的内容会被清掉。它不能比要镜像的盘小。",
  "nas.detachq": "把 {0} 从 {1} 里拿出来？数据还在别的盘上；只剩一块盘时，在再加一块盘之前存储没有第二份。",
  "nas.replace": "更换",
  "nas.replace.title": "更换 {0}",
  "nas.replace.hint": "新盘接替 {0}：存储把数据复制上去（resilver），完成后放掉旧盘。新盘上原有的内容会被清掉；它不能比旧盘小。",
  "nas.replace.missing": "{0}（不在了，原来是 {1}）",
  "nas.export": "导出",
  "nas.export.title": "导出 {0}",
  "nas.export.note": "存储连同数据离开 keelNAS，比如要把它的盘搬到另一台机器。要先删掉它上面的共享和 LUN。以后可以在\"盘上找到的池\"里重新导入。",
  "nas.delete": "删除",
  "nas.delete.title": "删除 {0}",
  "nas.delete.note": "这个存储上的一切都会被销毁，它的盘重新空出来。不能撤销。要先删掉它上面的共享和 LUN。",
  "nas.confirmname": "输入存储的名字（{0}）确认",
  "nas.expand.note": "这个存储还能大 {0}：它的盘比它用的大（大盘换了小盘）。",
  "nas.expandq": "把 {0} 扩到盘的全部大小（zpool online -e）？什么都不会被清掉；以后不能再缩小。",
  "nas.expanded": "{0} 现在是 {1}",
  "nas.labelclear": "清除标签",
  "nas.labelclear.title": "清除 {0} 上的 ZFS 标签",
  "nas.labelclear.note": "盘 {0} 上有池 {1} 的标签，这个池没有导入。清除后这个池在这块盘上的东西就没了；之后这块盘可以用来建存储。",
  "nas.labelclear.confirm": "输入 {0} 确认",
  "nas.devhelp": "加盘：数据多一份副本（单盘变镜像）。拿出：一块盘离开镜像（最后一块能用的盘不行）。更换：新盘接替不在了、坏了或者较小的盘。",
  "nas.pickdisks": "选一块盘，或者两块盘存同样的内容。",
  "nas.keepnote": "0 = 不做这一种。手动打的快照不会被自动删除。",
  "nas.newuser": "新建用户",
  "nas.newlun": "新建 LUN",
  "nas.noshares": "还没有共享。",
  "nas.noluns": "还没有 LUN。",
  "nas.tab.overview": "概览",
  "nas.tab.storage": "存储",
  "nas.tab.shares": "共享",
  "nas.tab.users": "用户",
  "nas.tab.iscsi": "iSCSI",
  "nas.alerts": "需要处理",
  "nas.alert.pool_degraded": "{0} 有一块盘不工作了：数据现在没有保护。请换盘。",
  "nas.alert.pool_no_protection": "{0} 只有一块盘：盘坏了数据就没了。加一块盘可以保护它。",
  "nas.alert.pool_full": "{0} 快满了。",
  "nas.alert.disk_failing": "盘 {0} 报告自己快坏了。请换掉。",
  "nas.alert.scrub_errors": "上一次检查 {0} 时发现了修不好的错误。",
  "nas.alert.apply_failed": "keelNAS 没能把设置落实：{0}",
  "nas.alert.snapshot_failed": "{0} 的快照没打成。",
  "nas.snaps": "快照",
  "nas.snapsof": "{0} 的快照",
  "nas.snapnow": "现在打一个",
  "nas.nosnaps": "还没有快照：有改动时每小时自动打一个。",
  "nas.snaptime": "时间",
  "nas.snapclass": "类型",
  "nas.class.hourly": "每小时",
  "nas.class.daily": "每天",
  "nas.class.weekly": "每周",
  "nas.class.manual": "手动",
  "nas.delsnapq": "删除 {0} 的这个快照？",
  "nas.snapnote": "在 Windows 里：右键点共享里的文件或文件夹，属性，以前的版本。",
  "nas.keep": "自动快照设定",
  "nas.keep.hourly": "每小时的",
  "nas.keep.daily": "每天的",
  "nas.keep.weekly": "每周的",
  "nas.save": "保存",
  "nas.saved": "已保存",
  "nas.smartbad": "快坏了",
  "nas.none": "这台主机上没有 keelNAS。",
  "nas.down": "keelNAS 没有应答。",
  "nas.retry": "再试一次",
  "nas.healthy": "正常",
  "nas.storage": "存储",
  "nas.storagen": "存储 {0}",
  "nas.nostorage": "还没有存储。",
  "nas.nodisks": "没有空闲的盘：先给 keelNAS 虚拟机加一块盘。",
  "nas.disk.busy": "keelNAS 不用的盘",
  "nas.disk.foreign": "属于没有导入的池 {0}：keelNAS 绝不往上写（要用里面的数据就导入这个池）",
  "nas.disk.destroyed": "上面有已销毁的池 {0}：数据还可能救回来，keelNAS 不往上写",
  "nas.disk.inpool": "在池 {0} 里",
  "nas.disk.parts": "不是空盘：有 {0} 分区",
  "nas.disk.fs": "不是空盘：有 {0} 文件系统",
  "nas.disk.data": "不是空盘：开头有数据",
  "nas.disk.unknown": "检查不了：不用",
  "nas.imp.found": "盘上找到的池",
  "nas.imp.what": "这些 ZFS 池是在别处建的，还没有导入。keelNAS 不碰它们的盘；要用就导入，数据原样保留。",
  "nas.imp.disks": "{0} 块盘",
  "nas.imp.raw": "原始容量",
  "nas.imp.lastby": "上次被另一个系统使用（{0}）",
  "nas.imp.btn": "导入",
  "nas.imp.go": "导入这个池",
  "nas.imp.title": "导入池 {0}",
  "nas.imp.note": "池和数据保持原样（不升级，不改任何设置）。它的文件系统挂在 keelNAS 里的 {0} 下面，原来的挂载点盖不住系统。和每个存储一样，keelNAS 会在池里存一份自己的设置。",
  "nas.imp.taken": "keelNAS 里已经有叫 {0} 的池：给这个池换个名字再导入（新名字会写进池里）。",
  "nas.imp.name": "名字",
  "nas.imp.otherhost": "这个池上次由另一个系统（{0}）使用。那个系统已经不再用这些盘：接管这个池。",
  "nas.protected": "有保护",
  "nas.unprotected": "没有保护",
  "nas.onhost": "在主机的存储上",
  "nas.addprotect": "用 {0}（{1}）加保护",
  "nas.protectq": "把 {0} 加进 {1} 做镜像？{0} 上原有的内容会被清掉。",
  "nas.newstorage": "新建存储",
  "nas.mirror": "保护数据：两块盘存同样的内容（推荐）",
  "nas.twoneedmirror": "不提供两块盘合在一起又没有保护的做法：请建两个存储。",
  "nas.make": "建立存储",
  "nas.users": "用户",
  "nas.usersnote": "谁能打开共享。这些不是 keelOS 的登录账号。",
  "nas.username": "用户名",
  "nas.password": "密码",
  "nas.adduser": "添加用户",
  "nas.delete": "删除",
  "nas.deluserq": "删除用户 {0}？它会从所有共享里去掉。",
  "nas.shares": "共享",
  "nas.share": "共享",
  "nas.address": "地址",
  "nas.rw": "可读写",
  "nas.ro": "只读",
  "nas.noaccess": "不能访问",
  "nas.manager": "管理",
  "nas.managernote": "管理的人对共享里的一切有完全控制，每个文件夹都进得去（不管它的名单怎么写），也能在 Windows 的“属性 → 安全”里改权限。",
  "nas.rootmanagers": "只有管理的人能在共享的根目录新建、改名、删除文件夹",
  "nas.folders": "文件夹",
  "nas.foldersof": "{0} 的文件夹",
  "nas.folder": "文件夹",
  "nas.fd.who": "谁能用",
  "nas.fd.likeshare": "和共享一样",
  "nas.fd.plus": "和共享一样，另加 {0}",
  "nas.fd.mgronly": "只有管理的人",
  "nas.fd.other": "另有在 Windows 里设的",
  "nas.fd.new": "新建文件夹",
  "nas.fd.make": "建立文件夹",
  "nas.fd.namehint": "Windows 接受的名字都行：不能有 \\ / : * ? \" < > |，不能以空格或点结尾。",
  "nas.fd.mode.share": "和共享一样：能用这个共享的人都能用",
  "nas.fd.mode.own": "只有这些人（和共享的管理的人）",
  "nas.fd.note": "单独设了名单的文件夹只有名单里的人进得去，其他人连看都看不到它。里面的东西跟着文件夹走。这就是文件夹真正的权限：Windows 的“属性 → 安全”里看到的是同一份，在那边改了这里也看得到。",
  "nas.fd.othernote": "这个文件夹有在 Windows 里设的、既不是“可读写”也不是“只读”的权限。在这里保存会把它们换掉。",
  "nas.fd.again": "重新套用",
  "nas.fd.againnote": "从别的文件夹移过来的文件带着原来的权限。“重新套用”让每个文件重新得到它所在文件夹的权限。",
  "nas.fd.delq": "删除文件夹 {0}？只删空的文件夹。",
  "nas.fd.none": "这个共享里还没有文件夹。",
  "nas.fd.oldkeel": "文件夹功能需要这台主机上更新的 keelOS。",
  "nas.acl.jobcount": "正在设权限：在数文件…",
  "nas.acl.jobrun": "正在设权限：{0} / {1}",
  "nas.acl.jobfailed": "设权限失败：{0}",
  "nas.used": "已用",
  "nas.newshare": "新建共享",
  "nas.sharename": "名字",
  "nas.addshare": "建立共享",
  "nas.sharenote": "每个共享都要用户名和密码：Windows 不接受对所有人开放的共享。",
  "nas.sharesneed": "建共享之前先要有一个存储和一个用户。",
  "nas.delshareq": "去掉共享 {0}？里面的文件留在存储上。",
  "nas.size": "大小",
  "nas.lunname": "名字",
  "nas.namehint": "小写字母、数字、- 和 _，以字母开头，最多 32 个字符（例如 lun0、backup-1）",
  "nas.addlun": "建立 LUN",
  "nas.dellunq": "去掉 LUN {0}？数据留在存储上。",
  "nas.mem": "内存空闲 {0} / {1}，缓存 {2}",
  "nas.dir.title": "来自 keelAuth 的账号",
  "nas.dir.what": "把这台 keelNAS 接到一台 keelAuth：它的用户和组就能用 keelAuth 的密码打开共享。改密码、停用、删除几秒内跟上。本地用户照旧。",
  "nas.dir.none": "这台主机上还没有 keelAuth。",
  "nas.dir.toaccounts": "账号",
  "nas.dir.connect": "接到 keelAuth",
  "nas.dir.connectgo": "接上",
  "nas.dir.from": "用户和组来自 {0}",
  "nas.dir.synced": "上次检查 {0}",
  "nas.dir.syncnow": "现在同步",
  "nas.dir.disconnect": "断开",
  "nas.dir.disconnectq": "和 {0} 断开",
  "nas.dir.whatbecomes": "这台 keelNAS 上来自 keelAuth 的用户怎么办？",
  "nas.dir.keep": "转成本地用户（能登录的保留密码；共享里的组换成它的成员）",
  "nas.dir.delete": "删掉（所有共享都不能再用）",
  "nas.dir.nousers": "keelAuth 里还没有用户。",
  "nas.dir.display": "姓名",
  "nas.dir.groups": "组",
  "nas.dir.state": "SMB",
  "nas.dir.ok": "能登录",
  "nas.dir.disabled": "在 keelAuth 里停用了",
  "nas.dir.nopw": "还没有 SMB 密码：在 keelAuth 里再设一次密码",
  "nas.dir.p.local_user": "没有建：有同名的本地用户",
  "nas.dir.p.system_account": "没有建：有同名的系统账号",
  "nas.dir.p.bad_uid": "没有建：用户号不能用",
  "nas.dir.p.bad_name": "没有建：名字不能用",
  "nas.dir.p.bad_gid": "没有建：组号不能用",
  "nas.dir.p.system_group": "没有建：有同名的系统组",
  "nas.dir.note": "用户和组在 keelAuth（账号标签）里改，不在这里。共享可以选它们；组写成 @名字。",
  "nas.dir.userlabel": "{0}（keelAuth）",
  "nas.dir.grouplabel": "组 {0}（keelAuth）",
  "nas.ds.dataset": "数据集",
  "nas.ds.auto": "自动快照",
  "nas.ds.autohint": "这个数据集每小时、每天、每周自动打快照（保留几个：列表下面的按钮）。共享的数据集默认开，别的打勾才开。",
  "nas.ds.autoon": "{0} 的自动快照已打开",
  "nas.ds.autooff": "{0} 的自动快照已关闭",
  "nas.ds.notmounted": "（没挂载）",
  "nas.ds.sharedas": "共享为 {0}",
  "nas.ds.share": "共享",
  "nas.ds.props": "共享属性",
  "nas.ds.browse": "浏览",
  "nas.ds.new": "新建数据集",
  "nas.ds.newin": "在 {0} 里新建数据集",
  "nas.ds.make": "建数据集",
  "nas.ds.delq": "删除数据集 {0}，连同里面所有的文件和快照？删了不能恢复。",
  "nas.ds.sharetitle": "共享 {0}",
  "nas.ds.browsetitle": "{0} 里的文件",
  "nas.ds.empty": "空的。",
  "nas.ds.trunc": "只显示了前 {0} 项。",
  "nas.ds.name": "名字",
  "nas.ds.modified": "修改时间",
  "nas.ds.link": "（链接）",
  "nas.ds.readonly": "这里只能看：改文件请通过共享。",
  "nas.ds.ownercol": "属主",
  "nas.ds.chown": "改属主",
  "nas.ds.chowntitle": "改 {0} 的属主",
  "nas.ds.chownuser": "新的属主",
  "nas.ds.chowngo": "改属主",
  "nas.ds.ownernowis": "它的文件现在属于 {0}。",
  "nas.ds.chownnote": "这个数据集里的每个文件和目录都改成属于这个用户和 keelnas 组：目录 2770、文件 0660，和 keelNAS 建的数据集一样。原来的属主和权限不保留。下面的数据集不动。数据集大时要一些时间，进度显示在这里。",
  "nas.ds.needowner": "它的文件不属于 keelNAS 的用户：先改属主。",
  "nas.ds.jobcount": "正在改属主：在数文件…",
  "nas.ds.jobrun": "正在改属主：{0} / {1}",
  "nas.ds.jobfailed": "改属主失败：{0}",
  "nas.net.old": "这台 keelNAS 是旧版本，没有网络设置。从市场重新建一台就有了。",
  "nas.make.name": "名字",
  "nas.instances": "这台主机上的 keelNAS",
  "nas.ok": "运行中",
  "nas.alertsn": "{0} 项需要处理",
  "nas.all": "全部 keelNAS",
  "nas.renameq": "新名字：",
  "nas.rename": "改名",
  "nas.deleteq": "删除 {0}？它的虚拟机和所有磁盘都会删掉，共享里的文件也一起没了，不能恢复。",
  "nas.deleteinst": "删除",
  "nas.off": "keelNAS 已关机：它的虚拟机没有在运行。",
  "nas.start": "启动 keelNAS",
  "nas.suspended": "keelNAS 已挂起。",
  "nas.resume": "恢复 keelNAS",
  "nas.missing": "这台 keelNAS 的虚拟机已经不在了（被删除）。",
  "nas.tab.network": "网络",
  "nas.tab.backups": "备份取回",
  "nas.ar.open": "检索邮件",
  "nas.ar.title": "{0} 备份里的邮件留存",
  "nas.ar.what": "keelMail 的留存（每封邮件，各留 30 天）在这里保存的各份备份里：最多往前一年。检索、查看、下载都有记录。",
  "nas.ar.audit": "审计日志",
  "nas.ar.noaudit": "还没有。",
  "nas.ar.a.search": "检索",
  "nas.ar.a.view": "查看",
  "nas.ar.a.download": "下载",
  "nas.ar.from_d": "从",
  "nas.ar.to_d": "到",
  "nas.ar.dir": "方向",
  "nas.ar.dir.all": "全部",
  "nas.ar.dir.in": "收到",
  "nas.ar.dir.out": "发出",
  "nas.ar.dir.internal": "社内",
  "nas.ar.from": "发件人",
  "nas.ar.to": "收件人",
  "nas.ar.subject": "主题",
  "nas.ar.att": "附件",
  "nas.ar.body": "正文包含",
  "nas.ar.search": "检索",
  "nas.ar.searching": "正在检索各份备份…",
  "nas.ar.n": "{0} 封",
  "nas.ar.more": "只显示最新的 {0} 封：请缩小条件",
  "nas.ar.missing": "{0} … {1} 还不在备份里：请在 keelMail 上检索。",
  "nas.ar.time": "时间",
  "nas.ar.nosubject": "（无主题）",
  "nas.ar.copy": "来自备份",
  "nas.ar.eml": "下载（.eml）",
  "nas.bk.what": "keelNAS 每晚用其他 keel 系统（keelMail、keelAuth…）的只读取回令牌取它们每天的备份，加密存在这里，按自己的规则保留。有人攻进那些系统也删不掉这里的。",
  "nas.bk.add": "添加源",
  "nas.bk.addgo": "检查并添加",
  "nas.bk.added": "已添加 {0}：到点第一次取（或点“现在取”）",
  "nas.bk.token": "取回令牌",
  "nas.bk.tokenhint": "在源上做：keelMail → 备份 → 由备份机来取 → 做取回令牌。keelNAS 现在就连过去检查。",
  "nas.bk.pool": "存储",
  "nas.bk.hour": "取的时间",
  "nas.bk.hourhint": "每天这个钟点（UTC：keelNAS 没有自己的时区；17:00 UTC 是日本的 02:00）。失败后每小时再试。",
  "nas.bk.newtoken": "新的取回令牌（空：不变）",
  "nas.bk.newtokenhint": "源上重新做了令牌之后。",
  "nas.bk.keep": "保留多久",
  "nas.bk.keepd": "每天的留几份",
  "nas.bk.keepm": "每月的留几份（每月第一份）",
  "nas.bk.keepnow": "这里保留：最新 {0} 份每天的，和最近 {1} 个月每月的第一份。",
  "nas.bk.source": "源",
  "nas.bk.state": "状态",
  "nas.bk.last": "上次取到",
  "nas.bk.copies": "份数",
  "nas.bk.oldest": "最早 {0}",
  "nas.bk.size": "大小",
  "nas.bk.running": "正在取",
  "nas.bk.paused": "暂停",
  "nas.bk.failed": "失败",
  "nas.bk.ok": "正常",
  "nas.bk.never": "还没取",
  "nas.bk.now": "现在取",
  "nas.bk.fetching": "开始取：在后台进行",
  "nas.bk.pause": "暂停",
  "nas.bk.resume": "继续",
  "nas.bk.delete": "移除",
  "nas.bk.delq": "不再取 {0}？",
  "nas.bk.deldataq": "同时删除这里保存的备份（{0}）？取消就保留。",
  "nas.bk.none": "还没有源。",
  "nas.bk.nopool": "先建存储：备份放在上面。",
  "nas.bk.keyset": "备份用只在这台 keelNAS 系统盘上的钥匙加密。",
  "nas.bk.keynone": "加密钥匙在第一次取的时候生成。",
  "nas.bk.showkey": "显示钥匙",
  "nas.bk.key": "备份加密钥匙",
  "nas.bk.keynote": "存到安全的地方（密码管理器、纸上）。这台 keelNAS 的系统盘丢了，备份只能用它打开。",
  "nas.bk.old": "这台 keelNAS 太旧，不能取备份：请升级。",
  "nas.tab.hardware": "硬件",
  "nas.hw.what": "这台 keelNAS 的每块盘和 SMART 现在的读数：健康、温度、通电时间、重映射和待映射扇区（NVMe 是寿命和介质错误）。点健康一格看这块盘报告的全部内容。打开页面时从盘上读，打开期间每分钟读一次；休眠的盘不唤醒，显示每日巡检时的值。",
  "nas.hw.old": "这台 keelNAS 太旧，没有硬件页：升级到 2026-10-14 以后的版本（概览里升级）。",
  "nas.hw.storage": "存储",
  "nas.hw.virtual": "虚拟盘",
  "nas.hw.ndisks": "{0} 块盘",
  "nas.hw.system": "系统",
  "nas.hw.standby": "待机",
  "nas.hw.standbytip": "盘在休眠，没有唤醒它；这是每日巡检时的值",
  "nas.hw.readat": "读于 {0}",
  "nas.net": "局域网",
  "nas.net.mode": "地址获取",
  "nas.net.dhcp": "DHCP（自动）",
  "nas.net.static": "固定地址",
  "nas.net.addr": "地址",
  "nas.net.gw": "网关",
  "nas.net.dns": "DNS 服务器",
  "nas.net.nic": "网卡",
  "nas.net.link": "已连接",
  "nas.net.nolink": "未连接",
  "nas.net.internal": "keelOS 通过自己的内部链路（{0}）管理 keelNAS，所以这里设错了也总能改回来。",
  "nas.net.nointernal": "这台 keelNAS 没有到 keelOS 的内部链路（是在有它之前建的）：keelOS 经局域网连它，地址设错就连不上了。从市场重新建一台就会有。",
  "nas.net.title": "keelNAS 的局域网设置",
  "nas.net.note": "保存几秒后生效。用旧地址访问共享的电脑需要改用新地址。",
  "nas.net.saved": "已保存，keelNAS 几秒后换成新地址。",
  "nas.hw.health": "健康",
  "nas.hw.h.tip": "点开看这块盘自己报告的全部信息（SMART、错误日志）",
  "nas.hw.realloc": "重映射/待映射",
  "nas.hw.mediaerr": "介质错误",
  "nas.hw.h.mediatip": "盘自己累计的“介质和数据完整性错误”次数（NVMe 健康日志）：读写时数据校验没通过的次数，出厂以来一直累加，不会清零",
  "nas.hw.disks": "磁盘",
  "nas.hw.name": "名称",
  "nas.hw.model": "型号",
  "nas.hw.serial": "序列号",
  "nas.hw.size": "容量",
  "nas.hw.temp": "温度",
  "nas.hw.hours": "通电",
  "nas.hw.wear": "寿命已用",
  "nas.hw.passed": "正常",
  "nas.hw.failed": "故障",
  "nas.hw.nosmart": "无 SMART",
  "nas.hw.h.nvmelog": "NVMe 健康日志（log page 2）",
  "nas.hw.h.title": "{0} 的健康详情"
 },
 "ja": {
  "nas.make.market": "マーケットで keelNAS を入手",
  "nas.what": "keelNAS は、このホストの仮想マシンで動く小さな NAS です。共有フォルダー（SMB）と iSCSI、スナップショット、保護されたストレージ。ここで管理します。",
  "nas.make.title": "keelNAS をデプロイ",
  "nas.make.go": "デプロイして起動",
  "nas.make.image": "keelNAS のバージョン",
  "nas.make.net": "ネットワーク",
  "nas.make.ram": "メモリ（GiB）",
  "nas.make.data": "データディスク（ホストのプール上、GiB、0 = なし）",
  "nas.make.note": "システムディスクは 20 GiB で、パッケージから作成します。ホストのディスクやコントローラーは、あとで仮想マシンのページから渡せます。",
  "nas.waiting": "keelNAS を起動しています。準備ができると自動で表示されます（通常 1 分以内）。",
  "nas.thevm": "仮想マシン",
  "nas.forget": "この keelNAS を解除",
  "nas.forgetq": "この keelNAS を解除しますか？仮想マシンとデータは残り、このページから管理しなくなります。",
  "nas.edit": "編集",
  "nas.editshare": "{0} を使えるユーザー",
  "nas.passwd": "パスワード変更",
  "nas.passwdof": "{0} の新しいパスワード",
  "nas.protectbtn": "保護する",
  "nas.protecttitle": "{0} を保護",
  "nas.protectnote": "このディスクがストレージのミラーになります。ディスクの内容は消去されます。",
  "scrub.every.auto": "30 日ごと",
  "nas.scrub.auto": "前回のチェックから 30 日後、{0}",
  "nas.scrub.utc": "UTC",
  "nas.scrub.note": "チェックはストレージ上の全データを読み、ミラーで直せるものを修復します。共有は使えますが、実行中は遅くなります。「30 日ごと」は前回のチェックから 30 日後に行います。時刻は UTC です（keelNAS には独自のタイムゾーンがありません）。",
  "nas.scrub.nobytes": "{0} かかりました（速度の記録なし）",
  "nas.scrub.left": "残り {0}",
  "nas.scrub.lastnone": "まだチェックしていません。",
  "nas.scrub.superseded": "終了を確認する前に次のスキャンに置き換わりました",
  "nas.dev.disk": "ディスク",
  "nas.dev.state": "状態",
  "nas.dev.size": "サイズ",
  "nas.attach.title": "{0} にディスクを追加",
  "nas.attach.hint": "ディスクはデータのもう一つのコピー（ミラー）になります。データがすべてコピーされ、ここに resilver として表示されます。ディスクの内容は消去されます。ミラーするディスク以上の大きさが必要です。",
  "nas.detachq": "{0} を {1} から外しますか？データは他のディスクに残ります。ディスクが一つになると、再び追加するまで二つ目のコピーはありません。",
  "nas.replace": "交換",
  "nas.replace.title": "{0} を交換",
  "nas.replace.hint": "新しいディスクが {0} の代わりになります。データがコピーされ（resilver）、終わると古いディスクは外れます。新しいディスクの内容は消去されます。同じ大きさ以上が必要です。",
  "nas.replace.missing": "{0}（見つからない、以前は {1}）",
  "nas.export": "エクスポート",
  "nas.export.title": "{0} をエクスポート",
  "nas.export.note": "ストレージをデータごと keelNAS から外します（ディスクを別のマシンに移すときなど）。先に共有と LUN を削除してください。「ディスク上で見つかったプール」から再びインポートできます。",
  "nas.delete": "削除",
  "nas.delete.title": "{0} を削除",
  "nas.delete.note": "このストレージ上のすべてが破棄され、ディスクは空きに戻ります。元に戻せません。先に共有と LUN を削除してください。",
  "nas.confirmname": "確認のためストレージ名（{0}）を入力",
  "nas.expand.note": "このストレージは {0} 大きくできます：ディスクが使用分より大きいです（小さいディスクを大きいものに交換した）。",
  "nas.expandq": "{0} をディスクの全容量まで拡張しますか（zpool online -e）？何も消去されません。元に縮小はできません。",
  "nas.expanded": "{0} は {1} になりました",
  "nas.labelclear": "ラベルを消去",
  "nas.labelclear.title": "{0} の ZFS ラベルを消去",
  "nas.labelclear.note": "ディスク {0} にはインポートされていないプール {1} のラベルがあります。消去するとそのプールのこのディスク上のデータは失われ、ディスクはストレージに使えるようになります。",
  "nas.labelclear.confirm": "確認のため {0} を入力",
  "nas.devhelp": "追加：データのコピーを一つ増やします（単一ディスクがミラーになります）。外す：ディスクをミラーから外します（最後に動いているディスクは不可）。交換：見つからない・故障・小さいディスクを新しいディスクに置き換えます。",
  "nas.pickdisks": "1 台、または同じ内容を保存する 2 台を選びます。",
  "nas.keepnote": "0 = その種類は作成しません。手動のスナップショットは自動では削除されません。",
  "nas.newuser": "ユーザーを作成",
  "nas.newlun": "LUN を作成",
  "nas.noshares": "共有はまだありません。",
  "nas.noluns": "LUN はまだありません。",
  "nas.tab.overview": "概要",
  "nas.tab.storage": "ストレージ",
  "nas.tab.shares": "共有",
  "nas.tab.users": "ユーザー",
  "nas.tab.iscsi": "iSCSI",
  "nas.alerts": "対応が必要",
  "nas.alert.pool_degraded": "{0} のディスクが 1 台動いていません。データは保護されていません。ディスクを交換してください。",
  "nas.alert.pool_no_protection": "{0} はディスク 1 台です。故障するとデータは失われます。2 台目を追加すると保護できます。",
  "nas.alert.pool_full": "{0} の空きがほとんどありません。",
  "nas.alert.disk_failing": "ディスク {0} が故障の兆候を報告しています。交換してください。",
  "nas.alert.scrub_errors": "{0} の前回の検査で修復できないエラーが見つかりました。",
  "nas.alert.apply_failed": "keelNAS が設定を反映できませんでした：{0}",
  "nas.alert.snapshot_failed": "{0} のスナップショットに失敗しました。",
  "nas.snaps": "スナップショット",
  "nas.snapsof": "{0} のスナップショット",
  "nas.snapnow": "今すぐ作成",
  "nas.nosnaps": "スナップショットはまだありません。変更があれば 1 時間ごとに自動で作成されます。",
  "nas.snaptime": "時刻",
  "nas.snapclass": "種類",
  "nas.class.hourly": "毎時",
  "nas.class.daily": "毎日",
  "nas.class.weekly": "毎週",
  "nas.class.manual": "手動",
  "nas.delsnapq": "{0} のこのスナップショットを削除しますか？",
  "nas.snapnote": "Windows では、共有内のファイルやフォルダーを右クリックし、プロパティ、以前のバージョン。",
  "nas.keep": "自動スナップショット設定",
  "nas.keep.hourly": "毎時",
  "nas.keep.daily": "毎日",
  "nas.keep.weekly": "毎週",
  "nas.save": "保存",
  "nas.saved": "保存しました",
  "nas.smartbad": "故障の兆候",
  "nas.none": "このホストに keelNAS はありません。",
  "nas.down": "keelNAS が応答しません。",
  "nas.retry": "再試行",
  "nas.healthy": "正常",
  "nas.storage": "ストレージ",
  "nas.storagen": "ストレージ {0}",
  "nas.nostorage": "ストレージはまだありません。",
  "nas.nodisks": "空きディスクがありません。先に keelNAS のゲストにディスクを追加してください。",
  "nas.disk.busy": "keelNAS が使わないディスク",
  "nas.disk.foreign": "インポートされていないプール {0} のディスク：keelNAS は書き込みません（データを使うにはプールをインポート）",
  "nas.disk.destroyed": "破棄されたプール {0} があります：データを復旧できる可能性があるため、keelNAS は書き込みません",
  "nas.disk.inpool": "プール {0} に属します",
  "nas.disk.parts": "空ではありません：{0} パーティションがあります",
  "nas.disk.fs": "空ではありません：{0} ファイルシステムがあります",
  "nas.disk.data": "空ではありません：先頭にデータがあります",
  "nas.disk.unknown": "確認できません：使いません",
  "nas.imp.found": "ディスク上のプール",
  "nas.imp.what": "ほかの場所で作られ、インポートされていない ZFS プールです。keelNAS はそのディスクに触れません。データごと使うにはインポートします。",
  "nas.imp.disks": "ディスク {0} 台",
  "nas.imp.raw": "（物理容量）",
  "nas.imp.lastby": "最後に別のシステム（{0}）で使われました",
  "nas.imp.btn": "インポート",
  "nas.imp.go": "プールをインポート",
  "nas.imp.title": "プール {0} のインポート",
  "nas.imp.note": "プールとデータはそのままです（アップグレードも設定変更もしません）。ファイルシステムは keelNAS 内の {0} の下にマウントされ、元のマウントポイントがシステムを覆うことはありません。ほかのストレージと同様、keelNAS は設定のコピーをプールに保存します。",
  "nas.imp.taken": "keelNAS には {0} という名前のプールがすでにあります：別の名前でインポートしてください（新しい名前はプールに書き込まれます）。",
  "nas.imp.name": "名前",
  "nas.imp.otherhost": "このプールは最後に別のシステム（{0}）で使われました。そのシステムはもうこれらのディスクを使っていません：プールを引き継ぎます。",
  "nas.protected": "保護あり",
  "nas.unprotected": "保護なし",
  "nas.onhost": "ホストのストレージ上",
  "nas.addprotect": "{0}（{1}）で保護する",
  "nas.protectq": "{0} を {1} のミラーとして追加しますか？{0} の内容は消去されます。",
  "nas.newstorage": "ストレージを作成",
  "nas.mirror": "データを保護する：2 台のディスクに同じ内容を保存（推奨）",
  "nas.twoneedmirror": "保護なしで 2 台をまとめることはできません。ストレージを 2 つ作成してください。",
  "nas.make": "ストレージを作成",
  "nas.users": "ユーザー",
  "nas.usersnote": "共有を開けるユーザーです。keelOS のログインアカウントではありません。",
  "nas.username": "ユーザー名",
  "nas.password": "パスワード",
  "nas.adduser": "ユーザーを追加",
  "nas.delete": "削除",
  "nas.deluserq": "ユーザー {0} を削除しますか？すべての共有から外されます。",
  "nas.shares": "共有",
  "nas.share": "共有",
  "nas.address": "アドレス",
  "nas.rw": "読み書き",
  "nas.ro": "読み取り専用",
  "nas.noaccess": "アクセス不可",
  "nas.manager": "管理者",
  "nas.managernote": "管理者は共有内のすべてをフルコントロールでき、リストに関係なくどのフォルダーにも入れます。Windows の「プロパティ → セキュリティ」でアクセス許可も変更できます。",
  "nas.rootmanagers": "共有のルートでフォルダーを作成・名前変更・削除できるのは管理者だけにする",
  "nas.folders": "フォルダー",
  "nas.foldersof": "{0} のフォルダー",
  "nas.folder": "フォルダー",
  "nas.fd.who": "使えるユーザー",
  "nas.fd.likeshare": "共有と同じ",
  "nas.fd.plus": "共有と同じ、加えて {0}",
  "nas.fd.mgronly": "管理者のみ",
  "nas.fd.other": "Windows で設定された項目あり",
  "nas.fd.new": "フォルダーを作成",
  "nas.fd.make": "フォルダーを作成",
  "nas.fd.namehint": "Windows で使える名前：\\ / : * ? \" < > | は使えません。末尾の空白とピリオドも不可。",
  "nas.fd.mode.share": "共有と同じ：この共有を使える全員",
  "nas.fd.mode.own": "次のユーザーだけ（と共有の管理者）",
  "nas.fd.note": "独自のリストを持つフォルダーには、リストにある人だけが入れます。ほかの人にはフォルダー自体が見えません。中身はフォルダーに従います。これはフォルダーの実際のアクセス許可です。Windows の「プロパティ → セキュリティ」に同じものが表示され、そこで変更した内容もここに表示されます。",
  "nas.fd.othernote": "このフォルダーには、Windows で設定された「読み書き」でも「読み取り専用」でもないアクセス許可があります。ここで保存すると置き換えられます。",
  "nas.fd.again": "再適用",
  "nas.fd.againnote": "ほかのフォルダーから移動したファイルは元のアクセス許可のままです。「再適用」で、すべてのファイルにそのフォルダーのアクセス許可を設定し直します。",
  "nas.fd.delq": "フォルダー {0} を削除しますか？空のフォルダーだけ削除できます。",
  "nas.fd.none": "この共有にはまだフォルダーがありません。",
  "nas.fd.oldkeel": "フォルダー機能には、このホストの keelOS の更新が必要です。",
  "nas.acl.jobcount": "アクセス許可を設定中：ファイルを数えています…",
  "nas.acl.jobrun": "アクセス許可を設定中：{0} / {1}",
  "nas.acl.jobfailed": "アクセス許可の設定に失敗しました：{0}",
  "nas.used": "使用量",
  "nas.newshare": "共有を作成",
  "nas.sharename": "名前",
  "nas.addshare": "共有を作成",
  "nas.sharenote": "共有にはユーザー名とパスワードが必要です。Windows は誰でも開ける共有を拒否します。",
  "nas.sharesneed": "共有を作る前に、ストレージとユーザーが必要です。",
  "nas.delshareq": "共有 {0} を外しますか？ファイルはストレージに残ります。",
  "nas.size": "サイズ",
  "nas.lunname": "名前",
  "nas.namehint": "英小文字・数字・- と _、先頭は英字、32 文字まで（例：lun0、backup-1）",
  "nas.addlun": "LUN を作成",
  "nas.dellunq": "LUN {0} を外しますか？データはストレージに残ります。",
  "nas.mem": "メモリ空き {0} / {1}、キャッシュ {2}",
  "nas.dir.title": "keelAuth のアカウント",
  "nas.dir.what": "この keelNAS を keelAuth につなぐと、そのユーザーとグループが keelAuth のパスワードで共有を開けます。パスワードの変更、無効化、削除は数秒で反映されます。ローカルユーザーはそのままです。",
  "nas.dir.none": "このホストにはまだ keelAuth がありません。",
  "nas.dir.toaccounts": "アカウント",
  "nas.dir.connect": "keelAuth につなぐ",
  "nas.dir.connectgo": "つなぐ",
  "nas.dir.from": "ユーザーとグループは {0} から",
  "nas.dir.synced": "最終確認 {0}",
  "nas.dir.syncnow": "今すぐ同期",
  "nas.dir.disconnect": "切断",
  "nas.dir.disconnectq": "{0} から切断",
  "nas.dir.whatbecomes": "この keelNAS にある keelAuth のユーザーをどうしますか？",
  "nas.dir.keep": "ローカルユーザーとして残す（ログインできる人はパスワードもそのまま。共有のグループはそのメンバーに置き換え）",
  "nas.dir.delete": "削除する（どの共有も使えなくなります）",
  "nas.dir.nousers": "keelAuth にはまだユーザーがいません。",
  "nas.dir.display": "名前",
  "nas.dir.groups": "グループ",
  "nas.dir.state": "SMB",
  "nas.dir.ok": "ログイン可",
  "nas.dir.disabled": "keelAuth で無効",
  "nas.dir.nopw": "SMB パスワードがまだありません：keelAuth でもう一度パスワードを設定してください",
  "nas.dir.p.local_user": "作成せず：同名のローカルユーザーがいます",
  "nas.dir.p.system_account": "作成せず：同名のシステムアカウントがあります",
  "nas.dir.p.bad_uid": "作成せず：ユーザー番号が使えません",
  "nas.dir.p.bad_name": "作成せず：名前が使えません",
  "nas.dir.p.bad_gid": "作成せず：グループ番号が使えません",
  "nas.dir.p.system_group": "作成せず：同名のシステムグループがあります",
  "nas.dir.note": "ユーザーとグループは keelAuth（アカウントタブ）で変更します。共有で選べます。グループは @名前 と書きます。",
  "nas.dir.userlabel": "{0}（keelAuth）",
  "nas.dir.grouplabel": "グループ {0}（keelAuth）",
  "nas.ds.dataset": "データセット",
  "nas.ds.auto": "自動スナップショット",
  "nas.ds.autohint": "このデータセットのスナップショットを毎時・毎日・毎週自動で作成します（保存数は一覧の下のボタンで設定）。共有されているデータセットは既定でオン、それ以外はチェックするとオンになります。",
  "nas.ds.autoon": "{0} の自動スナップショットをオンにしました",
  "nas.ds.autooff": "{0} の自動スナップショットをオフにしました",
  "nas.ds.notmounted": "（マウントされていません）",
  "nas.ds.sharedas": "{0} として共有",
  "nas.ds.share": "共有",
  "nas.ds.props": "共有の設定",
  "nas.ds.browse": "閲覧",
  "nas.ds.new": "新しいデータセット",
  "nas.ds.newin": "{0} に新しいデータセット",
  "nas.ds.make": "データセットを作成",
  "nas.ds.delq": "データセット {0} を、中のファイルとスナップショットごと削除しますか？元に戻せません。",
  "nas.ds.sharetitle": "{0} を共有",
  "nas.ds.browsetitle": "{0} のファイル",
  "nas.ds.empty": "空です。",
  "nas.ds.trunc": "最初の {0} 件だけを表示しています。",
  "nas.ds.name": "名前",
  "nas.ds.modified": "更新日時",
  "nas.ds.link": "（リンク）",
  "nas.ds.readonly": "見るだけです。ファイルは共有から変更してください。",
  "nas.ds.ownercol": "所有者",
  "nas.ds.chown": "所有者を変更",
  "nas.ds.chowntitle": "{0} の所有者を変更",
  "nas.ds.chownuser": "新しい所有者",
  "nas.ds.chowngo": "所有者を変更",
  "nas.ds.ownernowis": "ファイルは現在 {0} のものです。",
  "nas.ds.chownnote": "このデータセットのすべてのファイルとフォルダーを、このユーザーと keelnas グループのものにします（フォルダー 2770、ファイル 0660、keelNAS が作るデータセットと同じ）。元の所有者と権限は残りません。下のデータセットはそのままです。大きなデータセットでは時間がかかり、進み具合はここに表示されます。",
  "nas.ds.needowner": "ファイルが keelNAS のユーザーのものではありません。先に所有者を変更してください。",
  "nas.ds.jobcount": "所有者を変更中：ファイルを数えています…",
  "nas.ds.jobrun": "所有者を変更中：{0} / {1}",
  "nas.ds.jobfailed": "所有者の変更に失敗しました：{0}",
  "nas.net.old": "この keelNAS は古いバージョンで、ネットワーク設定がありません。マーケットから作り直すと使えます。",
  "nas.make.name": "名前",
  "nas.instances": "このホストの keelNAS",
  "nas.ok": "実行中",
  "nas.alertsn": "{0} 件の要対応",
  "nas.all": "すべての keelNAS",
  "nas.renameq": "新しい名前：",
  "nas.rename": "名前を変更",
  "nas.deleteq": "{0} を削除しますか？仮想マシンとすべてのディスク、共有内のファイルも削除され、元に戻せません。",
  "nas.deleteinst": "削除",
  "nas.off": "keelNAS は停止しています：仮想マシンが動いていません。",
  "nas.start": "keelNAS を起動",
  "nas.suspended": "keelNAS は一時停止中です。",
  "nas.resume": "keelNAS を再開",
  "nas.missing": "この keelNAS の仮想マシンはありません（削除済み）。",
  "nas.tab.network": "ネットワーク",
  "nas.tab.backups": "バックアップ取得",
  "nas.ar.open": "メール検索",
  "nas.ar.title": "{0} のバックアップ内のメールアーカイブ",
  "nas.ar.what": "ここに保管しているバックアップに含まれる keelMail のアーカイブ（全メール、各 30 日分）を検索します：最大 1 年前まで。検索・閲覧・ダウンロードは記録されます。",
  "nas.ar.audit": "監査ログ",
  "nas.ar.noaudit": "まだありません。",
  "nas.ar.a.search": "検索",
  "nas.ar.a.view": "閲覧",
  "nas.ar.a.download": "ダウンロード",
  "nas.ar.from_d": "開始日",
  "nas.ar.to_d": "終了日",
  "nas.ar.dir": "方向",
  "nas.ar.dir.all": "すべて",
  "nas.ar.dir.in": "受信",
  "nas.ar.dir.out": "送信",
  "nas.ar.dir.internal": "社内",
  "nas.ar.from": "差出人",
  "nas.ar.to": "宛先",
  "nas.ar.subject": "件名",
  "nas.ar.att": "添付",
  "nas.ar.body": "本文に含む",
  "nas.ar.search": "検索",
  "nas.ar.searching": "バックアップを検索中…",
  "nas.ar.n": "{0} 通",
  "nas.ar.more": "最新の {0} 通だけ表示：条件を絞ってください",
  "nas.ar.missing": "{0} 〜 {1} はまだバックアップにありません：keelMail で検索してください。",
  "nas.ar.time": "日時",
  "nas.ar.nosubject": "（件名なし）",
  "nas.ar.copy": "取得元のコピー",
  "nas.ar.eml": "ダウンロード（.eml）",
  "nas.bk.what": "keelNAS は他の keel システム（keelMail、keelAuth など）の毎日のバックアップを、読み取り専用の取得キーで毎晩取りに行き、ここに暗号化して自分のルールで保管します。それらのシステムに侵入されても、ここのデータは消せません。",
  "nas.bk.add": "取得元を追加",
  "nas.bk.addgo": "確認して追加",
  "nas.bk.added": "{0} を追加しました：指定時刻に初回取得します（または「今すぐ取得」）",
  "nas.bk.token": "取得キー",
  "nas.bk.tokenhint": "取得元で作ります：keelMail → バックアップ → バックアップ機による取得 → 取得キーを作成。keelNAS が今すぐ接続して確認します。",
  "nas.bk.pool": "ストレージ",
  "nas.bk.hour": "取得時刻",
  "nas.bk.hourhint": "毎日この時刻（UTC：keelNAS は独自のタイムゾーンを持ちません。17:00 UTC は日本の 02:00）。失敗したら 1 時間ごとに再試行します。",
  "nas.bk.newtoken": "新しい取得キー（空欄：変更しない）",
  "nas.bk.newtokenhint": "取得元でキーを作り直したとき。",
  "nas.bk.keep": "保管期間",
  "nas.bk.keepd": "毎日のコピーの保管数",
  "nas.bk.keepm": "毎月のコピーの保管数（各月の最初のもの）",
  "nas.bk.keepnow": "ここでの保管：最新 {0} 日分と、直近 {1} か月の各月最初の 1 つ。",
  "nas.bk.source": "取得元",
  "nas.bk.state": "状態",
  "nas.bk.last": "前回の取得",
  "nas.bk.copies": "コピー数",
  "nas.bk.oldest": "最古 {0}",
  "nas.bk.size": "サイズ",
  "nas.bk.running": "取得中",
  "nas.bk.paused": "停止中",
  "nas.bk.failed": "失敗",
  "nas.bk.ok": "正常",
  "nas.bk.never": "未取得",
  "nas.bk.now": "今すぐ取得",
  "nas.bk.fetching": "取得を開始しました（バックグラウンド）",
  "nas.bk.pause": "一時停止",
  "nas.bk.resume": "再開",
  "nas.bk.delete": "削除",
  "nas.bk.delq": "{0} の取得をやめますか？",
  "nas.bk.deldataq": "ここに保管しているコピー（{0}）も削除しますか？キャンセルで残します。",
  "nas.bk.none": "取得元はまだありません。",
  "nas.bk.nopool": "先にストレージを作ってください：コピーはそこに保管します。",
  "nas.bk.keyset": "コピーはこの keelNAS のシステムディスクにだけある鍵で暗号化されています。",
  "nas.bk.keynone": "暗号化の鍵は初回取得時に作られます。",
  "nas.bk.showkey": "鍵を表示",
  "nas.bk.key": "バックアップの暗号化鍵",
  "nas.bk.keynote": "安全な場所（パスワード管理ツール、紙）に保管してください。この keelNAS のシステムディスクを失った場合、コピーはこの鍵でしか開けません。",
  "nas.bk.old": "この keelNAS は古いためバックアップを取得できません：アップグレードしてください。",
  "nas.tab.hardware": "ハードウェア",
  "nas.hw.what": "この keelNAS の各ディスクと SMART の現在の値：状態、温度、通電時間、代替処理済み／保留セクタ（NVMe は寿命とメディアエラー）。状態の欄を押すとディスクが報告する内容を全部見られます。ページを開いたときにディスクから読み、開いている間は毎分読み直します。スリープ中のディスクは起こさず、日次点検時の値を表示します。",
  "nas.hw.old": "この keelNAS は古く、ハードウェアページがありません。2026-10-14 以降の版に更新してください（概要から）。",
  "nas.hw.storage": "ストレージ",
  "nas.hw.virtual": "仮想ディスク",
  "nas.hw.ndisks": "ディスク {0} 台",
  "nas.hw.system": "システム",
  "nas.hw.standby": "スタンバイ",
  "nas.hw.standbytip": "ディスクはスリープ中で起こしていません。日次点検時の値です",
  "nas.hw.readat": "{0} に読み取り",
  "nas.net": "LAN",
  "nas.net.mode": "アドレスの取得",
  "nas.net.dhcp": "DHCP（自動）",
  "nas.net.static": "固定アドレス",
  "nas.net.addr": "アドレス",
  "nas.net.gw": "ゲートウェイ",
  "nas.net.dns": "DNS サーバー",
  "nas.net.nic": "ネットワークカード",
  "nas.net.link": "接続中",
  "nas.net.nolink": "未接続",
  "nas.net.internal": "keelOS は専用の内部リンク（{0}）で keelNAS を管理するため、ここで設定を誤っても必ず元に戻せます。",
  "nas.net.nointernal": "この keelNAS には keelOS への内部リンクがありません（導入前に作成されたため）。keelOS は LAN 経由で接続しており、アドレスを誤ると接続できなくなります。マーケットから作り直すとリンクが付きます。",
  "nas.net.title": "keelNAS の LAN 設定",
  "nas.net.note": "保存の数秒後に反映されます。旧アドレスで共有を使っているコンピューターは新しいアドレスに変更してください。",
  "nas.net.saved": "保存しました。keelNAS は数秒後に新しいアドレスになります。",
  "nas.hw.health": "状態",
  "nas.hw.h.tip": "このディスクが報告するすべての情報（SMART、エラーログ）を表示",
  "nas.hw.realloc": "代替処理済/保留",
  "nas.hw.mediaerr": "メディアエラー",
  "nas.hw.h.mediatip": "ディスクが累積している「メディアおよびデータ整合性エラー」の回数（NVMe ヘルスログ）。読み書き時にデータの検証に失敗した回数で、出荷時からの累計です",
  "nas.hw.disks": "ディスク",
  "nas.hw.name": "名前",
  "nas.hw.model": "モデル",
  "nas.hw.serial": "シリアル番号",
  "nas.hw.size": "容量",
  "nas.hw.temp": "温度",
  "nas.hw.hours": "通電時間",
  "nas.hw.wear": "寿命消費",
  "nas.hw.passed": "正常",
  "nas.hw.failed": "異常",
  "nas.hw.nosmart": "SMART なし",
  "nas.hw.h.nvmelog": "NVMe ヘルスログ（log page 2）",
  "nas.hw.h.title": "{0} の状態の詳細"
 }
};
function t(k, ...a) {
  const s = (words[getLang()] || words.en)[k] ?? words.en[k];
  if (s === undefined) return keelT(k, ...a);
  return a.length ? s.replace(/\{(\d)\}/g, (_, i) => String(a[+i])) : s;
}

const fail = (e) => toast(e.message, 8000);
// NB: the API base of the keelNAS instance on screen (/v1/nas/nas1); keel forwards it to that
// instance's keelnasd (keelNAS docs/05 §7). Set when an instance is rendered.
let NB = "/v1/nas/nas1";
const names = (xs) => (xs && xs.length ? xs.join(", ") : "–");
const enc = encodeURIComponent;
const namePattern = "[a-z][a-z0-9_-]{0,31}";
// freeName: the first of base, base1, base2… (or base0, base1… with zero) that taken has not
const freeName = (base, taken, zero) => {
  const have = new Set((taken || []).map((x) => x.name));
  for (let i = zero ? 0 : 1; ; i++) { const n = i === 0 || (!zero && i === 1) ? (zero ? base + i : base) : base + i; if (!have.has(n)) return n; }
};
// tr: a translated text when there is one for the key, else the fallback (an alert code or a
// snapshot class that this page does not know yet).
const tr = (k, fb, ...a) => { const v = t(k, ...a); return v === k ? fb : v; };

// Head: a card's title with its "new" button on the right; info: the card's explanation behind
// an Info at the right edge (user 2026-10-06).
const Head = ({ title, label, onNew, info }) => html`<div class="row-head"><h3>${title}</h3>${info && html`<span class="spacer"></span>`}
  ${label && html`<button class="primary" onClick=${onNew}>${label}</button>`}${info && html`<${Info}>${info}<//>`}</div>`;

// Health: ONLINE in green, anything else as it is.
function Health({ h }) {
  return html`<span class=${"badge" + (h === "ONLINE" ? " ok" : "")}>${h === "ONLINE" ? t("nas.healthy") : h}</span>`;
}
// on_host: the storage is the host's virtual disks; protecting them is the host's business (it
// warns there), so neither "not protected" nor a Protect button here.
const Protected = ({ p }) => !p.protected && p.on_host ? html`<span class="badge">${t("nas.onhost")}</span>`
  : html`<span class=${"badge" + (p.protected ? " ok" : "")}>${p.protected ? t("nas.protected") : t("nas.unprotected")}</span>`;
const diskText = (d) => d.name + " · " + gib(d.size) + (d.model ? " · " + d.model : "") + (d.serial ? " · " + d.serial : "");

// Alerts: what keelnasd says is wrong right now (keelNAS docs/04 §9.5), by code.
function Alerts({ tick }) {
  const [as, setAs] = useState([]);
  const [pools, setPools] = useState([]);
  useEffect(() => {
    api("GET", NB + "/alerts").then((r) => setAs(r.alerts || [])).catch(() => setAs([]));
    api("GET", NB + "/storage").then((r) => setPools((r.storage || []).map((p) => p.name))).catch(() => setPools([]));
  }, [tick]);
  if (as.length === 0) return null;
  // a storage is "Storage 1" on this page, not its pool name
  const obj = (o) => (pools.indexOf(o) >= 0 ? t("nas.storagen", pools.indexOf(o) + 1) : o);
  return html`<div class="card"><h3>${t("nas.alerts")}</h3>
    ${as.map((a) => html`<p class=${a.level === "error" ? "error" : "warn"}>${tr("nas.alert." + a.code, a.text, obj(a.object))}</p>`)}</div>`;
}

// NewStorage: one disk, or two as a mirror.
function NewStorage({ free, onClose, onDone }) {
  const [pick, setPick] = useState({});
  const [protect, setProtect] = useState(true);
  const chosen = free.filter((d) => pick[d.name]).map((d) => d.name);
  const bad = chosen.length === 0 || chosen.length > 2 || (chosen.length === 2 && !protect);
  const make = async () => { await api("POST", NB + "/storage", { disks: chosen, protect: chosen.length === 2 && protect }); onDone(); };
  return html`<${FormDialog} title=${t("nas.newstorage")} submit=${t("nas.make")} disabled=${bad} onSubmit=${make} onClose=${onClose}>
    <p class="muted small">${t("nas.pickdisks")}</p>
    ${free.map((d) => html`<label class="check"><input type="checkbox" checked=${!!pick[d.name]} onChange=${(e) => setPick({ ...pick, [d.name]: e.target.checked })} /> ${diskText(d)}</label>`)}
    ${chosen.length === 2 && html`<label class="check"><input type="checkbox" checked=${protect} onChange=${(e) => setProtect(e.target.checked)} /> ${t("nas.mirror")}</label>`}
    ${chosen.length === 2 && !protect && html`<p class="warn small">${t("nas.twoneedmirror")}</p>`}
  <//>`;
}

// isFree: a disk storage may use. keelnasd says so in "free" (keelNAS docs/04 §4.1: no pool on
// it, imported or not, no partitions, no file system, zeros at its start); an older keelnasd has
// no "free" and only knows imported pools.
const isFree = (d) => (d.free === undefined ? !d.pool && !d.system : d.free);
// busyText: why keelNAS does not use a disk that is in none of its storages.
function busyText(d) {
  const c = d.content || "";
  if (d.pool_state === "not_imported") return t("nas.disk.foreign", d.pool);
  if (d.pool_state === "destroyed") return t("nas.disk.destroyed", d.pool);
  if (d.pool) return t("nas.disk.inpool", d.pool);
  if (c.startsWith("partitions ")) return t("nas.disk.parts", c.slice(11));
  if (c.startsWith("filesystem ")) return t("nas.disk.fs", c.slice(11));
  if (c === "data") return t("nas.disk.data");
  return t("nas.disk.unknown");
}

// ImportPool: take over a pool that is on the disks, with its data (keelNAS docs/04 §4.1).
function ImportPool({ pool, onClose, onDone }) {
  const [name, setName] = useState(pool.name_taken ? "" : pool.name);
  const [sure, setSure] = useState(false);
  const bad = (pool.other_host && !sure) || !name;
  const go = async () => {
    const r = await api("POST", NB + "/importable/" + enc(pool.id) + "/import", { name: name === pool.name ? "" : name, force: !!pool.other_host });
    if (r && r.warning) toast(r.warning, 10000);
    onDone();
  };
  return html`<${FormDialog} title=${t("nas.imp.title", pool.name)} submit=${t("nas.imp.go")} disabled=${bad} onSubmit=${go} onClose=${onClose}>
    <p>${t("nas.imp.note", "/pools/" + (name || pool.name))}</p>
    ${pool.state !== "ONLINE" && html`<p class="warn small">${pool.state}: ${pool.status} ${pool.action}</p>`}
    ${pool.name_taken && html`<p class="warn small">${t("nas.imp.taken", pool.name)}</p>`}
    ${(pool.name_taken || name !== pool.name) && html`<label>${t("nas.imp.name")}<input value=${name} pattern=${namePattern} onInput=${(e) => setName(e.target.value)} /></label>`}
    ${pool.other_host && html`<label class="check"><input type="checkbox" checked=${sure} onChange=${(e) => setSure(e.target.checked)} /> ${t("nas.imp.otherhost", pool.last_host || "?")}</label>`}
  <//>`;
}

// AddProtection: a second disk for a single-disk storage.
function AddProtection({ pool, label, free, onClose, onDone }) {
  const [disk, setDisk] = useState(free[0].name);
  const go = async () => { await api("POST", NB + "/storage/" + enc(pool.name) + "/protect", { disk }); onDone(); };
  return html`<${FormDialog} title=${t("nas.protecttitle", label)} submit=${t("nas.protectbtn")} onSubmit=${go} onClose=${onClose}>
    ${free.map((d) => html`<label class="check"><input type="radio" name="nasdisk" checked=${disk === d.name} onChange=${() => setDisk(d.name)} /> ${diskText(d)}</label>`)}
    <p class="warn small">${t("nas.protectnote")}</p>
  <//>`;
}

// ---- a storage's pool features (keelNAS docs/04 §17), ported from keelOS's storage page
// (storage.js Scrub, ScrubPlan, the attach and detach controls; scrubperf.js ScrubLive and
// ScrubHistory without the chart, which is not in the contract with keel) ----

// Help: a "?" that opens its explanation (keel's ui.js Help is Info with this mark)
const Help = ({ children }) => html`<${Info} mark="?" cls="help">${children}<//>`;
const p2 = (v) => String(v).padStart(2, "0");
// fmtDur: seconds as "2:43" or "1:02:43" (keel's scrubperf.js)
function fmtDur(s) {
  s = Math.max(0, Math.round(s || 0));
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), x = s % 60;
  return h ? `${h}:${p2(m)}:${p2(x)}` : `${m}:${p2(x)}`;
}
const fmtRate = (v) => (v ? gib(v) + "/s" : "–");
const utcHour = (h) => p2(h) + ":00 " + t("nas.scrub.utc");
const scanBusy = (sc) => !!sc && (sc.state === "running" || sc.state === "paused");

// ScanProgress: a running (or paused) scrub or resilver: bar, how much, speed, time left, and
// each disk's read rate when the watch has samples (GET …/scrub/perf live.devs)
function ScanProgress({ sc, devs }) {
  const head = sc.state === "paused" ? t("scrub.paused") : t("scrub.running." + (sc.kind || "scrub"));
  const left = sc.state === "running" && sc.eta_s >= 0 ? t("nas.scrub.left", fmtDur(sc.eta_s)) : sc.state === "running" ? t("scrub.noeta") : "";
  const top = Math.max(1, ...(devs || []).map((d) => Math.max(d.now, d.avg)));
  return html`<div class="scrub-live">
    <div class="small"><b>${head}</b> · ${(sc.pct || 0).toFixed(1)}% · ${t("scrub.ofbytes", gib(sc.issued), gib(sc.total))} · ${fmtRate(sc.rate)}${left ? " · " + left : ""}</div>
    <${Bar} pct=${sc.pct || 0} />
    ${devs && devs.length > 0 && html`<table class="data plain small scrub-devs"><tbody>${devs.map((d) => html`<tr>
      <td class="mono">${d.name}</td><td class="scrub-devbar"><${Bar} pct=${(100 * d.now) / top} /></td><td class="num">${t("scrub.devnow", fmtRate(d.now), fmtRate(d.avg))}</td></tr>`)}</tbody></table>`}
  </div>`;
}

const runResult = (r) => (r.superseded ? t("nas.scrub.superseded") : r.canceled ? t("scrub.canceled") : r.errors ? t("scrub.found", r.errors, gib(r.repaired)) : r.repaired ? t("scrub.fixed", gib(r.repaired)) : t("scrub.clean"));

// Scrub: the storage's check: the plan and the next one, now / stop, the running one or the
// last ones (keel's Scrub and ScrubHistory)
function Scrub({ pool, sc, admin, tick, onChange }) {
  const [st, setSt] = useState(null);
  const [perf, setPerf] = useState(null);
  const [edit, setEdit] = useState(false);
  const [hist, setHist] = useState(false);
  const base = NB + "/storage/" + enc(pool) + "/scrub";
  useEffect(() => {
    api("GET", base).then(setSt).catch(() => setSt(null)); // an older keelnasd: no plan, no buttons
    api("GET", base + "/perf").then(setPerf).catch(() => setPerf(null));
  }, [tick, pool]);
  if (!st) return null;
  const go = async (what) => {
    try { await api("POST", base + "/" + what); onChange(); } catch (e) { fail(e); }
  };
  const s = st.schedule || {};
  const plan = s.every === "off" ? t("scrub.off") : s.every === "auto" ? t("nas.scrub.auto", utcHour(s.hour))
    : t(s.every === "week" ? "scrub.weekly" : "scrub.monthly", t("scrub.wd." + s.weekday), utcHour(s.hour));
  const runs = (perf && perf.runs) || [];
  const done = runs.filter((r) => !r.canceled && !r.superseded);
  const last = done[done.length - 1];
  const scan = sc || st.scan || {};
  return html`<div class="pool-scrub">
    <div class="row-head"><b class="small">${t("scrub.title")}</b>
      <span class="small muted">${plan}${s.once ? " · " + t("scrub.once", fmtTime(s.once)) : ""}${st.next ? " · " + t("scrub.next", fmtTime(st.next)) : ""}</span>
      <span class="spacer"></span>
      ${admin && html`<span>${scanBusy(scan) && scan.kind === "scrub"
        ? html`<button class="btn" onClick=${() => go("stop")}>${t("scrub.stop")}</button>`
        : html`<button class="btn" disabled=${scanBusy(scan)} onClick=${() => go("start")}>${t("scrub.now")}</button>`}
        <button class="btn" onClick=${() => setEdit(true)}>${t("scrub.plan")}…</button></span>`}
      <${Help}><p>${t("nas.scrub.note")}</p>${!last && html`<p>${t("scrub.nohist")}</p>`}<//></div>
    ${scanBusy(scan) && scan.kind === "scrub" ? html`<${ScanProgress} sc=${scan} devs=${perf && perf.live && perf.live.devs} />`
      : last ? html`<div class="small"><span class="muted">${t("scrub.lastat", fmtTime(last.end))}:</span> ${last.bytes && last.avg
          ? t("scrub.lastsum", gib(last.bytes), fmtDur(last.seconds), fmtRate(last.avg), fmtRate(last.peak))
          : t("nas.scrub.nobytes", fmtDur(last.seconds))} · <span class=${last.errors ? "error" : ""}>${runResult(last)}</span>
          ${runs.length > 1 && html` <button type="button" class="ghost small" onClick=${() => setHist(!hist)}>${t("scrub.history")} (${runs.length}) ${hist ? "▴" : "▾"}</button>`}</div>`
      : scan.kind === "scrub" && scan.state === "finished" ? html`<div class="small muted">${t("scrub.lastat", fmtTime(scan.end))}: ${fmtDur(scan.seconds)} · ${scan.errors ? t("scrub.found", scan.errors, gib(scan.repaired)) : t("scrub.clean")}</div>`
      : html`<div class="small muted">${t("nas.scrub.lastnone")}</div>`}
    ${hist && html`<div class="table-wrap"><table class="data plain small">
      <thead><tr><th>${t("scrub.col.when")}</th><th>${t("scrub.col.took")}</th><th>${t("scrub.col.size")}</th><th>${t("scrub.col.avg")}</th><th>${t("scrub.col.peak")}</th><th>${t("scrub.col.result")}</th></tr></thead>
      <tbody>${[...runs].reverse().map((r) => html`<tr><td>${fmtTime(r.end)}${r.kind === "resilver" ? " · resilver" : ""}</td>
        <td>${fmtDur(r.seconds)}</td><td>${r.bytes ? gib(r.bytes) : "–"}</td><td>${fmtRate(r.avg)}</td><td>${fmtRate(r.peak)}</td>
        <td class=${r.errors ? "error" : ""}>${runResult(r)}</td></tr>`)}</tbody></table></div>`}
    ${edit && html`<${ScrubPlan} base=${base} s=${s} onClose=${() => { setEdit(false); onChange(); }} />`}
  </div>`;
}

// ScrubPlan: auto (keelNAS's 30-day rule), weekly, monthly or off at an hour (UTC), and once.
// datetime-local is read as UTC too (keelNAS's clock), so what is typed is what keelnasd keeps.
function ScrubPlan({ base, s, onClose }) {
  const [every, setEvery] = useState(s.every || "auto");
  const [wd, setWd] = useState(s.weekday || 0);
  const [hour, setHour] = useState(s.hour === undefined ? 3 : s.hour);
  const [once, setOnce] = useState(s.once ? s.once.slice(0, 16) : "");
  const save = async () => {
    const body = { every, weekday: Number(wd), hour: Number(hour) };
    if (once) {
      const d = new Date(once + ":00Z");
      if (isNaN(d) || d <= new Date()) throw new Error(t("scrub.past"));
      body.once = d.toISOString();
    }
    await api("PUT", base, body);
  };
  return html`<${FormDialog} title=${t("scrub.plantitle")} submit=${t("scrub.save")} onSubmit=${save} onClose=${onClose}>
    <label>${t("scrub.every")}<select value=${every} onChange=${(e) => setEvery(e.target.value)}>
      ${["auto", "month", "week", "off"].map((k) => html`<option value=${k}>${t("scrub.every." + k)}</option>`)}</select></label>
    ${(every === "month" || every === "week") && html`<label>${t(every === "month" ? "scrub.wdmonth" : "scrub.wdweek")}<select value=${wd} onChange=${(e) => setWd(e.target.value)}>
      ${[1, 2, 3, 4, 5, 6, 0].map((d) => html`<option value=${d}>${t("scrub.wd." + d)}</option>`)}</select></label>`}
    ${every !== "off" && html`<label>${t("scrub.hour")} (${t("nas.scrub.utc")})<select value=${hour} onChange=${(e) => setHour(e.target.value)}>
      ${Array.from({ length: 24 }, (_, h) => html`<option value=${h}>${p2(h)}:00</option>`)}</select></label>`}
    <label>${t("scrub.oncelabel")} (${t("nas.scrub.utc")})<input type="datetime-local" value=${once} onInput=${(e) => setOnce(e.target.value)} /></label>
    <p class="muted small">${t("nas.scrub.note")}</p>
  <//>`;
}

// AttachDisk: one more copy for a single disk or a mirror (POST …/attach); disks smaller than
// the storage's smallest disk are greyed (keelnasd refuses them too).
function AttachDisk({ pool, label, free, onClose, onDone }) {
  const need = Math.min(...(pool.disks || []).filter((d) => !d.missing && d.size > 0).map((d) => d.size));
  const fits = (d) => !isFinite(need) || d.size >= need;
  const [disk, setDisk] = useState((free.find(fits) || {}).name || "");
  const go = async () => { await api("POST", NB + "/storage/" + enc(pool.name) + "/attach", { disk }); onDone(); };
  return html`<${FormDialog} title=${t("nas.attach.title", label)} submit=${t("pool.attach")} disabled=${!disk} onSubmit=${go} onClose=${onClose}>
    ${free.map((d) => html`<label class="check"><input type="radio" name="nasdisk" disabled=${!fits(d)} checked=${disk === d.name} onChange=${() => setDisk(d.name)} /> ${diskText(d)}${fits(d) ? "" : " · " + t("pool.attach.small", gib(need))}</label>`)}
    <p class="muted small">${t("nas.attach.hint")}</p>
  <//>`;
}

// ReplaceDisk: a new disk takes the place of a member (POST …/replace {old: its guid})
function ReplaceDisk({ pool, dev, free, onClose, onDone }) {
  const sibs = (pool.disks || []).filter((d) => d.vdev === dev.vdev && d.guid !== dev.guid && !d.missing && d.size > 0).map((d) => d.size);
  const need = sibs.length ? Math.min(...sibs) : dev.size || 0;
  const fits = (d) => !need || d.size >= need;
  const [disk, setDisk] = useState((free.find(fits) || {}).name || "");
  const name = dev.missing ? t("nas.replace.missing", dev.name, dev.was) : dev.name;
  const go = async () => { await api("POST", NB + "/storage/" + enc(pool.name) + "/replace", { old: dev.guid, disk }); onDone(); };
  return html`<${FormDialog} title=${t("nas.replace.title", name)} submit=${t("nas.replace")} disabled=${!disk} onSubmit=${go} onClose=${onClose}>
    ${free.map((d) => html`<label class="check"><input type="radio" name="nasdisk" disabled=${!fits(d)} checked=${disk === d.name} onChange=${() => setDisk(d.name)} /> ${diskText(d)}${fits(d) ? "" : " · " + t("pool.attach.small", gib(need))}</label>`)}
    <p class="muted small">${t("nas.replace.hint", name)}</p>
  <//>`;
}

// ConfirmName: export or delete a storage, confirmed with its name
function ConfirmName({ title, note, want, submit, onGo, onClose }) {
  const [v, setV] = useState("");
  return html`<${FormDialog} title=${title} submit=${submit} danger=${true} disabled=${v !== want} onSubmit=${onGo} onClose=${onClose}>
    <p>${note}</p>
    <label>${t("nas.confirmname", want)}<input value=${v} onInput=${(e) => setV(e.target.value)} autocomplete="off" /></label>
  <//>`;
}

// LabelClear: a disk with the label of a pool that is not imported (POST /v1/disks/{d}/labelclear)
function LabelClear({ d, onClose, onDone }) {
  const want = !d.pool || d.pool === "(zfs)" ? d.name : d.pool;
  const [v, setV] = useState("");
  const go = async () => { await api("POST", NB + "/disks/" + enc(d.name) + "/labelclear", { confirm: v }); onDone(); };
  return html`<${FormDialog} title=${t("nas.labelclear.title", d.name)} submit=${t("nas.labelclear")} danger=${true} disabled=${v !== want} onSubmit=${go} onClose=${onClose}>
    <p>${t("nas.labelclear.note", d.name, d.pool)}</p>
    <label>${t("nas.labelclear.confirm", want)}<input value=${v} onInput=${(e) => setV(e.target.value)} autocomplete="off" /></label>
  <//>`;
}

// Devices: a storage's disks with their state and the per-disk actions
function Devices({ p, admin, free, failing, onAct }) {
  const ds = p.disks || [];
  const mirrorOf = (d) => (p.vdevs || []).some((v) => v.name === d.vdev && v.type === "mirror");
  const working = (d) => !d.missing && ["ONLINE", "DEGRADED"].includes(d.state);
  const canDetach = (d) => mirrorOf(d) && ds.filter((x) => x.vdev === d.vdev).length > 1 && (!working(d) || ds.filter((x) => x.vdev === d.vdev && working(x)).length > 1);
  return html`<table class="data plain small"><thead><tr><th>${t("nas.dev.disk")}</th><th>${t("nas.dev.state")}</th><th>${t("nas.dev.size")}</th>
      <th class="num">${admin && html`<${Help}>${t("nas.devhelp")}<//>`}</th></tr></thead>
    <tbody>${ds.map((d) => html`<tr>
      <td class="mono">${d.missing ? html`<span title=${t("pool.missing.tip", d.name)}>${d.was} <span class="error">(${t("pool.missing")})</span></span>` : d.name}${mirrorOf(d) ? html` <span class="muted">${d.vdev}</span>` : ""}${failing(d.name) ? html` <span class="error">${t("nas.smartbad")}</span>` : null}</td>
      <td class=${d.state === "ONLINE" ? "" : "error"}>${d.state}</td><td>${d.size ? gib(d.size) : "–"}</td>
      <td class="num">${admin && html`${canDetach(d) && html`<button class="btn" title=${t("pool.detach.tip")} onClick=${() => onAct("detach", d)}>${t("pool.detach")}</button>`}
        ${free.length > 0 && !scanBusy(p.scan) && html` <button class="btn" onClick=${() => onAct("replace", d)}>${t("nas.replace")}…</button>`}`}</td></tr>`)}</tbody></table>`;
}

function Storage({ admin, onChange }) {
  const [st, setSt] = useState(null);
  const [disks, setDisks] = useState([]);
  const [imp, setImp] = useState([]); // pools on the disks that are not imported
  const [trees, setTrees] = useState({}); // pool -> its datasets; null: an older keelnasd without them
  const [users, setUsers] = useState([]);
  const [jobs, setJobs] = useState([]); // owner changes (keelnasd's background jobs)
  // {kind: "new"} | {kind: "protect", pool, label} | {kind: "import", pool} | {kind: "share" | "props" | "browse" | "newds", pool, ds}
  const [dlg, setDlg] = useState(null);
  const load = () => Promise.all([api("GET", NB + "/storage"), api("GET", NB + "/disks"),
    api("GET", NB + "/importable").catch(() => ({ pools: [] })), // an older keelnasd has no importable
    api("GET", NB + "/users").catch(() => ({}))])
    .then(async ([s, d, i, u]) => {
      const ps = s.storage || [];
      // the dataset tree of each storage (keelNAS docs/06); an older keelnasd has none: the page stays as it was
      const ts = await Promise.all(ps.map((p) => api("GET", dsPath(p.name)).then((r) => r.datasets || []).catch(() => null)));
      const js = await api("GET", NB + "/jobs").then((r) => r.jobs || []).catch(() => []);
      setTrees(Object.fromEntries(ps.map((p, k) => [p.name, ts[k]])));
      setJobs(js);
      setSt(ps); setDisks(d.disks || []); setImp(i.pools || []); setUsers(accessList(u));
    }).catch(fail);
  useEffect(() => { load(); }, []);
  // an owner change runs: look again every few seconds for its progress
  const running = Object.values(trees).some((tr) => (tr || []).some((d) => d.job));
  // a scrub or resilver runs: its progress every few seconds too
  const scanning = (st || []).some((p) => scanBusy(p.scan));
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (!running && !scanning) return undefined;
    const h = setTimeout(() => { load(); setTick((x) => x + 1); }, running ? 2000 : 4000);
    return () => clearTimeout(h);
  }, [trees, st]);
  if (!st) return html`<${Spinner} />`;
  const free = disks.filter(isFree);
  const inStorage = new Set(st.flatMap((p) => (p.disks || []).map((d) => d.name)));
  const busy = disks.filter((d) => !isFree(d) && !d.system && !inStorage.has(d.name));
  const failing = (n) => disks.some((d) => d.name === n && d.smart === "failing");
  const done = () => { load(); setTick((x) => x + 1); onChange(); };
  const detach = async (p, label, d) => {
    if (!await ask(t("nas.detachq", d.missing ? d.was : d.name, label))) return;
    try { await api("POST", NB + "/storage/" + enc(p.name) + "/detach", { disk: d.guid }); toast(t("pool.detached", d.missing ? d.was : d.name)); } catch (e) { fail(e); }
    done();
  };
  const expand = async (p, label) => {
    if (!await ask(t("nas.expandq", label))) return;
    try { const r = await api("POST", NB + "/storage/" + enc(p.name) + "/expand"); toast(t("nas.expanded", label, gib(r.storage && r.storage.size))); } catch (e) { fail(e); }
    done();
  };
  const setAuto = async (pool, ds, on) => {
    try { await api("PUT", dsPath(pool, ds.name) + "/auto-snapshot", { on }); toast(t(on ? "nas.ds.autoon" : "nas.ds.autooff", ds.name)); } catch (e) { fail(e); }
    load();
  };
  const delDs = async (pool, ds) => {
    if (!await ask(t("nas.ds.delq", ds.name))) return;
    try { await api("DELETE", dsPath(pool, ds.name)); done(); } catch (e) { fail(e); }
  };
  return html`<div class="card"><${Head} title=${t("nas.storage")} label=${admin && free.length > 0 && t("nas.newstorage")} onNew=${() => setDlg({ kind: "new" })} />
    ${st.length === 0 && html`<p class="muted">${t("nas.nostorage")}${free.length === 0 ? " " + t("nas.nodisks") : ""}</p>`}
    ${st.map((p, i) => html`<div class="stack" style="margin-bottom:14px">
      <div><b>${t("nas.storagen", i + 1)}</b> <${Health} h=${p.health} /> <${Protected} p=${p} />
        <span class="muted small"> ${gib(p.used)} / ${gib(p.size)}</span>
        <span class="muted small"> · ${p.name}</span>
        ${admin && html`<span class="actions-inline">
          ${!p.protected && !p.on_host && (p.disks || []).length === 1 && free.length > 0 ? html` <button class="btn" onClick=${() => setDlg({ kind: "protect", pool: p, label: t("nas.storagen", i + 1) })}>${t("nas.protectbtn")}</button>`
            : p.vdevs && p.vdevs.length === 1 && p.vdevs[0].type === "mirror" && free.length > 0 && html` <button class="btn" title=${t("pool.attach.tip")} onClick=${() => setDlg({ kind: "attach", pool: p, label: t("nas.storagen", i + 1) })}>${t("pool.attach")}…</button>`}
          ${p.vdevs && html` <button class="btn" onClick=${() => setDlg({ kind: "export", pool: p, label: t("nas.storagen", i + 1) })}>${t("nas.export")}…</button>
            <button class="btn danger" onClick=${() => setDlg({ kind: "delete", pool: p, label: t("nas.storagen", i + 1) })}>${t("nas.delete")}…</button>`}</span>`}</div>
      <${Bar} pct=${p.size ? (100 * p.used) / p.size : 0} />
      ${p.vdevs ? html`<${Devices} p=${p} admin=${admin} free=${free} failing=${failing}
          onAct=${(kind, d) => (kind === "detach" ? detach(p, t("nas.storagen", i + 1), d) : setDlg({ kind: "replace", pool: p, dev: d }))} />`
        : html`<div class="small muted">${(p.disks || []).map((d, j) => html`${j ? ", " : ""}${d.name}${d.state !== "ONLINE" ? " (" + d.state + ")" : ""}${failing(d.name) ? html` <span class="error">${t("nas.smartbad")}</span>` : null}`)}</div>`}
      ${scanBusy(p.scan) && p.scan.kind === "resilver" && html`<${ScanProgress} sc=${p.scan} />`}
      ${p.expand > 0 && html`<div class="small warn">${t("nas.expand.note", gib(p.expand))}${admin && html` <button class="btn" onClick=${() => expand(p, t("nas.storagen", i + 1))}>${t("pool.expand")}…</button>`}</div>`}
      ${p.vdevs && p.health !== "MISSING" && html`<${Scrub} pool=${p.name} sc=${p.scan} admin=${admin} tick=${tick} onChange=${done} />`}
      ${trees[p.name] && html`<${DatasetTree} pool=${p.name} label=${t("nas.storagen", i + 1)} tree=${trees[p.name]} jobs=${jobs} admin=${admin}
        onAct=${(kind, ds, on) => (kind === "del" ? delDs(p.name, ds) : kind === "auto" ? setAuto(p.name, ds, on) : setDlg({ kind, pool: p.name, ds, tree: trees[p.name] }))} />`}
    </div>`)}
    ${st.length > 0 && admin && html`<div class="actions"><button class="btn" onClick=${() => setDlg({ kind: "keep" })}>${t("nas.keep")}…</button></div>`}
    ${imp.length > 0 && html`<h3>${t("nas.imp.found")}</h3>
      <p class="muted small">${t("nas.imp.what")}</p>
      ${imp.map((p) => html`<div class="stack" style="margin-bottom:14px">
        <div><b>${p.name}</b> <span class=${"badge" + (p.state === "ONLINE" ? " ok" : "")}>${p.state}</span>
          <span class="muted small"> ${p.layout} · ${t("nas.imp.disks", (p.disks || []).length)} · ${gib(p.size)} ${t("nas.imp.raw")}</span>
          ${p.other_host && html` <span class="warn small">${t("nas.imp.lastby", p.last_host || "?")}</span>`}
          ${admin && html` <button class="btn" onClick=${() => setDlg({ kind: "import", pool: p })}>${t("nas.imp.btn")}</button>`}</div>
        <div class="small muted">${(p.disks || []).map((d, j) => html`${j ? ", " : ""}${d.name || d.device}${d.state !== "ONLINE" ? " (" + d.state + ")" : ""}`)}</div>
      </div>`)}`}
    ${busy.length > 0 && html`<h3>${t("nas.disk.busy")}</h3>
      ${busy.map((d) => html`<div class="small">${diskText(d)} — <span class="muted">${busyText(d)}</span>
        ${admin && (d.pool_state === "not_imported" || d.pool_state === "destroyed") && html` <button class="btn" onClick=${() => setDlg({ kind: "labelclear", disk: d })}>${t("nas.labelclear")}…</button>`}</div>`)}`}
    ${dlg && dlg.kind === "new" && html`<${NewStorage} free=${free} onClose=${() => setDlg(null)} onDone=${done} />`}
    ${dlg && dlg.kind === "import" && html`<${ImportPool} pool=${dlg.pool} onClose=${() => setDlg(null)} onDone=${done} />`}
    ${dlg && dlg.kind === "protect" && html`<${AddProtection} pool=${dlg.pool} label=${dlg.label} free=${free} onClose=${() => setDlg(null)} onDone=${done} />`}
    ${dlg && dlg.kind === "attach" && html`<${AttachDisk} pool=${dlg.pool} label=${dlg.label} free=${free} onClose=${() => setDlg(null)} onDone=${done} />`}
    ${dlg && dlg.kind === "replace" && html`<${ReplaceDisk} pool=${dlg.pool} dev=${dlg.dev} free=${free} onClose=${() => setDlg(null)} onDone=${done} />`}
    ${dlg && dlg.kind === "labelclear" && html`<${LabelClear} d=${dlg.disk} onClose=${() => setDlg(null)} onDone=${done} />`}
    ${dlg && dlg.kind === "export" && html`<${ConfirmName} title=${t("nas.export.title", dlg.label)} note=${t("nas.export.note")} want=${dlg.pool.name} submit=${t("nas.export")}
      onGo=${async () => { await api("POST", NB + "/storage/" + enc(dlg.pool.name) + "/export", { confirm: dlg.pool.name }); done(); }} onClose=${() => setDlg(null)} />`}
    ${dlg && dlg.kind === "delete" && html`<${ConfirmName} title=${t("nas.delete.title", dlg.label)} note=${t("nas.delete.note")} want=${dlg.pool.name} submit=${t("nas.delete")}
      onGo=${async () => { await api("DELETE", NB + "/storage/" + enc(dlg.pool.name) + "?confirm=" + enc(dlg.pool.name)); done(); }} onClose=${() => setDlg(null)} />`}
    ${dlg && dlg.kind === "share" && html`<${ShareForm} dataset=${dlg.ds} shares=${sharesOf(dlg.tree)} users=${users} pools=${[]} onClose=${() => setDlg(null)} onDone=${done} />`}
    ${dlg && dlg.kind === "props" && html`<${ShareForm} share=${dlg.ds.share} users=${users} pools=${[]} onClose=${() => setDlg(null)} onDone=${done} />`}
    ${dlg && dlg.kind === "newds" && html`<${NewDataset} pool=${dlg.pool} parent=${dlg.ds} tree=${dlg.tree} onClose=${() => setDlg(null)} onDone=${done} />`}
    ${dlg && dlg.kind === "browse" && html`<${Browse} pool=${dlg.pool} ds=${dlg.ds} onClose=${() => setDlg(null)} />`}
    ${dlg && dlg.kind === "keep" && html`<${Keep} onClose=${() => setDlg(null)} />`}
    ${dlg && dlg.kind === "chown" && html`<${ChangeOwner} pool=${dlg.pool} ds=${dlg.ds} users=${users} onClose=${() => setDlg(null)} onDone=${done} />`}
  </div>`;
}

// ---- datasets (keelNAS docs/06): every ZFS file system of a storage, any of them can be
// shared; only those keelNAS made can be deleted; a read-only look at the files ----

// dsPath: the API path of a storage's datasets, or of one (its whole name in one segment)
const dsPath = (pool, name) => NB + "/storage/" + enc(pool) + "/datasets" + (name ? "/" + enc(name) : "");
const lastSeg = (n) => n.slice(n.lastIndexOf("/") + 1);
const depth = (n) => n.split("/").length - 1;
const sharesOf = (tree) => (tree || []).filter((d) => d.share).map((d) => d.share);

// ownerText: who owns a dataset's folder: its keelNAS user, root, or the bare uid.
const ownerText = (o) => (!o ? "" : o.user || (o.uid === 0 ? "root" : "uid " + o.uid));
// jobText: an owner change in words (running: how far; failed: why).
const jobText = (j) => { const k = j.kind === "acl" ? "nas.acl.job" : "nas.ds.job";
  return j.state === "counting" ? t(k + "count") : j.state === "running" ? t(k + "run", j.files, j.total) : j.state === "failed" ? t(k + "failed", j.error) : ""; };

// DatasetTree: a storage's datasets as an indented list (the name below its parent). A dataset
// keelNAS did not make is shared only once its files belong to a keelNAS user ("change owner").
function DatasetTree({ label, tree, jobs, admin, onAct }) {
  // the last owner change of each dataset, to say when it failed
  const last = {};
  for (const j of jobs || []) last[j.dataset] = j;
  // a keelnasd before 2026-10-08 has no per-dataset auto snapshots: no column
  const auto = tree.some((d) => "auto_snapshot" in d);
  return html`<div class="table-wrap tall"><table class="data plain ds-tree">
    <thead><tr><th>${t("nas.ds.dataset")}</th><th>${t("nas.used")}</th><th>${t("nas.ds.ownercol")}</th><th>${t("nas.share")}</th>
      ${auto && html`<th title=${t("nas.ds.autohint")}>${t("nas.ds.auto")}</th>`}<th></th></tr></thead>
    <tbody>${tree.map((d) => {
      const root = depth(d.name) === 0;
      const j = d.job || (last[d.name] && last[d.name].state === "failed" ? last[d.name] : null);
      return html`<tr>
      <td class="ds-name" style=${"padding-left:" + (8 + 18 * depth(d.name)) + "px"} title=${d.name + (d.mountpoint ? " · " + d.mountpoint : "")}>${root ? label : lastSeg(d.name)}${!d.mounted ? html` <span class="muted small">${t("nas.ds.notmounted")}</span>` : null}</td>
      <td class="ds-used">${gib(d.used)}</td>
      <td class="ds-owner" data-label=${t("nas.ds.ownercol")}>${ownerText(d.owner)}${j && html`<div class=${"small " + (j.state === "failed" ? "error" : "muted")}>${jobText(j)}</div>`}</td>
      <td class=${"ds-share" + (d.share ? "" : " none")}>${d.share ? t("nas.ds.sharedas", d.share.name) : null}</td>
      ${auto && html`<td class="ds-auto" data-label=${t("nas.ds.auto")} title=${t("nas.ds.autohint")}>${admin
        ? html`<input type="checkbox" aria-label=${t("nas.ds.auto")} checked=${!!d.auto_snapshot} onChange=${(e) => onAct("auto", d, e.target.checked)} />`
        : d.auto_snapshot ? "✓" : "–"}</td>`}
      <td class="ds-acts">${admin && d.share && html`<button class="btn" onClick=${() => onAct("props", d)}>${t("nas.ds.props")}</button>${" "}`}
        ${admin && !d.share && d.mounted && html`<button class="btn" disabled=${!d.shareable || !!d.job} title=${d.shareable ? "" : t("nas.ds.needowner")} onClick=${() => onAct("share", d)}>${t("nas.ds.share")}</button>${" "}`}
        ${d.mounted && html`<button class="btn" onClick=${() => onAct("browse", d)}>${t("nas.ds.browse")}</button>${" "}`}
        ${admin && d.mounted && !root && html`<button class="btn" disabled=${!!d.job} onClick=${() => onAct("chown", d)}>${t("nas.ds.chown")}</button>${" "}`}
        ${admin && html`<button class="btn" onClick=${() => onAct("newds", d)}>${t("nas.ds.new")}</button>${" "}`}
        ${admin && d.ours && !d.share && html`<button class="btn danger" onClick=${() => onAct("del", d)}>${t("nas.delete")}</button>`}</td></tr>`;
    })}</tbody></table></div>`;
}

// ChangeOwner: give every file of a dataset to one keelNAS user (keelnasd's background job:
// user:keelnas, folders 2770, files 0660, as on a dataset keelNAS makes).
function ChangeOwner({ pool, ds, users, onClose, onDone }) {
  const people = users.filter((u) => !u.name.startsWith("@"));
  const [user, setUser] = useState(ds.owner && ds.owner.user ? ds.owner.user : (people[0] || {}).name || "");
  const go = async () => { await api("POST", dsPath(pool, ds.name) + "/owner", { user }); onDone(); };
  return html`<${FormDialog} title=${t("nas.ds.chowntitle", ds.name)} submit=${t("nas.ds.chowngo")} disabled=${!user} onSubmit=${go} onClose=${onClose}>
    <p class="muted small">${t("nas.ds.ownernowis", ownerText(ds.owner))}</p>
    <label>${t("nas.ds.chownuser")}<select value=${user} onChange=${(e) => setUser(e.target.value)}>
      ${people.map((u) => html`<option value=${u.name}>${u.label || u.name}</option>`)}</select></label>
    <p class="warn small">${t("nas.ds.chownnote")}</p>
  <//>`;
}

// NewDataset: a dataset below parent (not shared yet).
function NewDataset({ pool, parent, tree, onClose, onDone }) {
  const [name, setName] = useState(() => freeName("data", (tree || []).filter((d) => d.name.startsWith(parent.name + "/")).map((d) => ({ name: lastSeg(d.name) })), false));
  const make = async () => { await api("POST", dsPath(pool), { parent: parent.name, name: name.trim() }); onDone(); };
  return html`<${FormDialog} title=${t("nas.ds.newin", parent.name)} submit=${t("nas.ds.make")} onSubmit=${make} onClose=${onClose}>
    <label>${t("nas.sharename")}<input type="text" required pattern=${namePattern} title=${t("nas.namehint")} value=${name} onInput=${(e) => setName(e.target.value)} /><span class="muted small">${t("nas.namehint")}</span></label>
  <//>`;
}

// Browse: a read-only look into a dataset: a path bar and the entries of one directory.
function Browse({ pool, ds, onClose }) {
  const [path, setPath] = useState("");
  const [r, setR] = useState(null);
  const [err, setErr] = useState("");
  useEffect(() => {
    setR(null); setErr("");
    api("GET", dsPath(pool, ds.name) + "/files?path=" + enc(path)).then(setR).catch((e) => setErr(e.message));
  }, [path]);
  const segs = path ? path.split("/") : [];
  const go = (k) => setPath(segs.slice(0, k).join("/"));
  const into = (e) => setPath(path ? path + "/" + e.name : e.name);
  return html`<${Dialog} title=${t("nas.ds.browsetitle", ds.name)} wide=${true} onClose=${onClose}><div class="stack">
    <div class="mono small"><button type="button" class="linklike" onClick=${() => go(0)}>${ds.mountpoint || ds.name}</button>${segs.map((x, k) => html` / ${k === segs.length - 1 ? x : html`<button type="button" class="linklike" onClick=${() => go(k + 1)}>${x}</button>`}`)}</div>
    ${err && html`<p class="error small">${err}</p>`}
    ${!r && !err && html`<${Spinner} small=${true} />`}
    ${r && (r.entries || []).length === 0 && html`<p class="muted small">${t("nas.ds.empty")}</p>`}
    ${r && (r.entries || []).length > 0 && html`<div class="table-wrap tall"><table class="data plain">
      <thead><tr><th>${t("nas.ds.name")}</th><th>${t("nas.size")}</th><th>${t("nas.ds.modified")}</th></tr></thead>
      <tbody>${r.entries.map((e) => html`<tr>
        <td>${e.dir ? html`<button type="button" class="linklike" onClick=${() => into(e)}>${e.name}/</button>` : e.name}${e.link ? html` <span class="muted small">${t("nas.ds.link")}</span>` : null}</td>
        <td>${e.dir ? "" : bytes(e.size)}</td><td>${fmtTime(e.mtime)}</td></tr>`)}</tbody></table></div>`}
    ${r && r.truncated && html`<p class="muted small">${t("nas.ds.trunc", (r.entries || []).length)}</p>`}
    <p class="muted small">${t("nas.ds.readonly")}</p></div><//>`;
}

// bytes: a file size in B, KiB, MiB …
const bytes = (n) => {
  const u = ["B", "KiB", "MiB", "GiB", "TiB"];
  let i = 0;
  for (n = n || 0; n >= 1024 && i < u.length - 1; i++) n /= 1024;
  return (i ? n.toFixed(1) : String(n)) + " " + u[i];
};

// Access: each user of a share: read and write, read only, or none. onChange gets an updater
// (a => new value), so changes that come quickly one after another all count. users: {name, label}: local
// users, keelAuth's users and its groups ("@office"), from accessList.
// manager: a share's list also offers "manager" (keelNAS docs/07); a folder's does not.
function Access({ users, value, onChange, manager }) {
  return users.map((u) => html`<label>${u.label || u.name}
    <select value=${value[u.name] || ""} onChange=${(e) => { const v = e.target.value; onChange((a) => ({ ...a, [u.name]: v })); }}>
      <option value="">${t("nas.noaccess")}</option><option value="rw">${t("nas.rw")}</option><option value="ro">${t("nas.ro")}</option>
      ${manager && html`<option value="mg">${t("nas.manager")}</option>`}
    </select></label>`);
}
const split = (acc) => ({ rw: Object.keys(acc).filter((u) => acc[u] === "rw"), ro: Object.keys(acc).filter((u) => acc[u] === "ro") });
const mgrs = (acc) => Object.keys(acc).filter((u) => acc[u] === "mg");
const join = (s) => Object.fromEntries([...(s.rw || []).map((u) => [u, "rw"]), ...(s.ro || []).map((u) => [u, "ro"]), ...(s.managers || []).map((u) => [u, "mg"])]);
// accessList: who can be named in a share (GET …/users): keelAuth's groups that keelNAS made, local
// users, keelAuth's users (docs/54 §3.3).
// Groups come first: with many people a share names groups, and the people are a long list.
const accessList = (r) => [
  ...(r.groups || []).filter((g) => !g.problem).map((g) => ({ name: "@" + g.name, label: t("nas.dir.grouplabel", g.name) })),
  ...(r.users || []).map((u) => ({ name: u.name })),
  ...(r.synced || []).filter((u) => !u.problem).map((u) => ({ name: u.name, label: t("nas.dir.userlabel", u.name) })),
];
const some = (acc) => split(acc).rw.length + split(acc).ro.length + mgrs(acc).length > 0;

// ShareForm: who may use an existing share (share); a new share of a dataset (dataset, keelNAS
// docs/06); or, with neither, a new share in a new dataset <storage>/shares/<name> (the old way,
// which an older keelnasd also knows).
function ShareForm({ share, dataset, shares, users, pools, onClose, onDone }) {
  const [name, setName] = useState(() => {
    if (share) return "";
    const have = new Set((shares || []).map((x) => x.name));
    const own = dataset && lastSeg(dataset.name);
    if (own && new RegExp("^" + namePattern + "$").test(own) && !have.has(own)) return own;
    return freeName("files", shares, false); // a free suggestion
  });
  const [pool, setPool] = useState(pools.length ? pools[0].name : "");
  const [acc, setAcc] = useState(share ? join(share) : {});
  const [rootM, setRootM] = useState(!!(share && share.root_managers));
  const save = async () => {
    const who = { ...split(acc), managers: mgrs(acc), root_managers: rootM && mgrs(acc).length > 0 };
    if (share) await api("PUT", NB + "/shares/" + enc(share.name), who);
    else if (dataset) await api("POST", NB + "/shares", { dataset: dataset.name, name: name.trim(), ...who });
    else await api("POST", NB + "/shares", { name: name.trim(), pool, ...who });
    onDone();
  };
  return html`<${FormDialog} title=${share ? t("nas.editshare", share.name) : dataset ? t("nas.ds.sharetitle", dataset.name) : t("nas.newshare")}
      submit=${share ? t("nas.save") : t("nas.addshare")} disabled=${!some(acc)} onSubmit=${save} onClose=${onClose}>
    ${!share && html`<label>${t("nas.sharename")}<input type="text" required pattern=${namePattern} title=${t("nas.namehint")} value=${name} onInput=${(e) => setName(e.target.value)} /><span class="muted small">${t("nas.namehint")}</span></label>`}
    ${!share && !dataset && pools.length > 1 && html`<label>${t("nas.storage")}<select value=${pool} onChange=${(e) => setPool(e.target.value)}>
      ${pools.map((p, i) => html`<option value=${p.name}>${t("nas.storagen", i + 1)}</option>`)}</select></label>`}
    <${Access} users=${users} value=${acc} onChange=${setAcc} manager=${true} />
    ${mgrs(acc).length > 0 && html`<label class="check"><input type="checkbox" checked=${rootM} onChange=${(e) => setRootM(e.target.checked)} /> ${t("nas.rootmanagers")}</label>
      <p class="muted small">${t("nas.managernote")}</p>`}
    <p class="muted small">${t("nas.sharenote")}</p>
  <//>`;
}

// Folders of a share (keelNAS docs/07): each folder in the share's root with who may use it.
// The lists are the folders' own permissions (NFSv4 ACLs), the ones Windows' security tab
// shows: nothing is kept beside them, so both sides always see the same.
const folderWho = (f) => {
  const own = [...(f.rw || []).map((n) => n + " (" + t("nas.rw") + ")"), ...(f.ro || []).map((n) => n + " (" + t("nas.ro") + ")")].join(", ");
  const base = f.inherit ? (own ? t("nas.fd.plus", own) : t("nas.fd.likeshare")) : (own || t("nas.fd.mgronly"));
  return base + (f.other ? " · " + t("nas.fd.other") : "");
};
function FolderForm({ base, folder, users, onClose, onDone }) {
  const [name, setName] = useState("");
  const [own, setOwn] = useState(!!folder && !folder.inherit);
  const [acc, setAcc] = useState(folder && !folder.inherit ? join(folder) : {});
  const save = async () => {
    const who = own ? split(acc) : (folder ? { inherit: true } : {});
    if (folder) await api("PUT", base + "/folders/" + enc(folder.name), who);
    else await api("POST", base + "/folders", { name: name.trim(), ...who });
    onDone();
  };
  return html`<${FormDialog} title=${folder ? t("nas.editshare", folder.name) : t("nas.fd.new")} submit=${folder ? t("nas.save") : t("nas.fd.make")} onSubmit=${save} onClose=${onClose}>
    ${!folder && html`<label>${t("nas.sharename")}<input type="text" required value=${name} onInput=${(e) => setName(e.target.value)} /><span class="muted small">${t("nas.fd.namehint")}</span></label>`}
    <label class="check"><input type="radio" name="fdmode" checked=${!own} onChange=${() => setOwn(false)} /> ${t("nas.fd.mode.share")}</label>
    <label class="check"><input type="radio" name="fdmode" checked=${own} onChange=${() => setOwn(true)} /> ${t("nas.fd.mode.own")}</label>
    ${own && html`<${Access} users=${users} value=${acc} onChange=${setAcc} />`}
    ${folder && folder.other && html`<p class="warn small">${t("nas.fd.othernote")}</p>`}
    <p class="muted small">${t("nas.fd.note")}</p>
  <//>`;
}
function Folders({ share, users, admin, onClose }) {
  const [fs, setFs] = useState(null);
  const [job, setJob] = useState(null);
  const [old, setOld] = useState(false);
  const [dlg, setDlg] = useState(null); // {folder} | {} (new)
  const base = NB + "/shares/" + enc(share.name);
  const load = () => api("GET", base + "/folders").then((r) => { setFs(r.folders || []); setJob(r.job || null); })
    .catch((e) => { if (e.status === 404) { setOld(true); setFs([]); } else fail(e); });
  useEffect(() => { load(); }, []);
  // while the files below are rewritten: look again until the job is gone
  useEffect(() => { if (!job) return undefined; const id = setTimeout(load, 2000); return () => clearTimeout(id); }, [job]);
  const del = async (f) => {
    if (!await ask(t("nas.fd.delq", f.name))) return;
    try { await api("DELETE", base + "/folders/" + enc(f.name)); load(); } catch (e) { fail(e); }
  };
  const again = async () => { try { setJob(await api("POST", base + "/acl")); } catch (e) { fail(e); } };
  if (dlg) return html`<${FolderForm} base=${base} folder=${dlg.folder} users=${users} onClose=${() => setDlg(null)} onDone=${() => { setDlg(null); setJob({ state: "counting", kind: "acl" }); load(); }} />`;
  return html`<${Dialog} title=${t("nas.foldersof", share.name)} onClose=${onClose}><div class="stack">
    ${!fs && html`<${Spinner} small=${true} />`}
    ${old && html`<p class="warn">${t("nas.fd.oldkeel")}</p>`}
    ${fs && !old && fs.length === 0 && html`<p class="muted small">${t("nas.fd.none")}</p>`}
    ${fs && fs.length > 0 && html`<div class="table-wrap tall"><table class="data plain">
      <thead><tr><th>${t("nas.folder")}</th><th>${t("nas.fd.who")}</th><th></th></tr></thead>
      <tbody>${fs.map((f) => html`<tr><td style="white-space:nowrap">${f.name}</td><td class=${f.inherit && !f.rw.length && !f.ro.length ? "muted" : ""} title=${(f.acl || []).join("\n")}>${folderWho(f)}</td>
        <td style="white-space:nowrap;text-align:right">${admin && html`<button type="button" class="btn" disabled=${!!job} onClick=${() => setDlg({ folder: f })}>${t("nas.edit")}</button>${" "}
          <button type="button" class="btn danger" onClick=${() => del(f)}>${t("nas.delete")}</button>`}</td></tr>`)}</tbody></table></div>`}
    ${job && html`<p class="small">${jobText(job)}</p>`}
    <p class="muted small">${t("nas.fd.note")}</p>
    ${admin && !old && html`<p class="muted small">${t("nas.fd.againnote")}</p>
      <div class="actions"><button type="button" class="btn" disabled=${!!job} onClick=${again}>${t("nas.fd.again")}</button>
        <button type="button" class="primary" disabled=${!!job} onClick=${() => setDlg({})}>${t("nas.fd.new")}</button></div>`}</div><//>`;
}

// Snapshots of one share: the list, take one now, delete one.
function Snapshots({ share, admin, onClose }) {
  const [ss, setSs] = useState(null);
  const base = NB + "/shares/" + enc(share) + "/snapshots";
  const load = () => api("GET", base).then((r) => setSs(r.snapshots || [])).catch(fail);
  useEffect(() => { load(); }, []);
  const take = async () => { try { await api("POST", base); load(); } catch (e) { fail(e); } };
  const del = async (x) => { try { await api("DELETE", base + "/" + enc(x.name)); load(); } catch (e) { fail(e); } };
  return html`<${Dialog} title=${t("nas.snapsof", share)} onClose=${onClose}><div class="stack">
    ${!ss && html`<${Spinner} small=${true} />`}
    ${ss && ss.length === 0 && html`<p class="muted small">${t("nas.nosnaps")}</p>`}
    ${ss && ss.length > 0 && html`<div class="table-wrap tall"><table class="data plain">
      <thead><tr><th>${t("nas.snaptime")}</th><th>${t("nas.snapclass")}</th><th>${t("nas.used")}</th><th></th></tr></thead>
      <tbody>${ss.map((x) => html`<tr><td>${fmtTime(x.time)}</td><td>${tr("nas.class." + x.class, x.class)}</td><td>${gib(x.used)}</td>
        <td>${admin && html`<button type="button" class="btn danger" onClick=${() => del(x)}>${t("nas.delete")}</button>`}</td></tr>`)}</tbody></table></div>`}
    <p class="muted small">${t("nas.snapnote")}</p>
    ${admin && html`<div class="actions"><button type="button" class="primary" onClick=${take}>${t("nas.snapnow")}</button></div>`}</div><//>`;
}

// Keep: how many automatic snapshots stay (0 = none of that kind).
function Keep({ onClose }) {
  const [k, setK] = useState(null);
  useEffect(() => { api("GET", NB + "/settings").then((r) => setK(r.snapshots || {})).catch((e) => { fail(e); onClose(); }); }, []);
  if (!k) return null;
  const save = async () => {
    await api("PUT", NB + "/settings", { snapshots: { hourly: Number(k.hourly) || 0, daily: Number(k.daily) || 0, weekly: Number(k.weekly) || 0 } });
    toast(t("nas.saved"));
  };
  return html`<${FormDialog} title=${t("nas.keep")} submit=${t("nas.save")} onSubmit=${save} onClose=${onClose}>
    ${["hourly", "daily", "weekly"].map((c) => html`<label>${t("nas.keep." + c)}
      <input type="number" min="0" max="999" value=${k[c]} onInput=${(e) => setK({ ...k, [c]: e.target.value })} /></label>`)}
    <p class="muted small">${t("nas.keepnote")}</p>
  <//>`;
}

function Shares({ admin, host, tick }) {
  const [sh, setSh] = useState(null);
  const [users, setUsers] = useState([]);
  const [pools, setPools] = useState([]);
  const [dlg, setDlg] = useState(null); // {kind: "new" | "edit" | "snaps", share}
  const load = () => Promise.all([api("GET", NB + "/shares"), api("GET", NB + "/users"), api("GET", NB + "/storage")])
    .then(([s, u, p]) => { setSh(s.shares || []); setUsers(accessList(u)); setPools(p.storage || []); }).catch(fail);
  useEffect(() => { load(); }, [tick]);
  if (!sh) return html`<${Spinner} />`;
  const del = async (s) => {
    if (!await ask(t("nas.delshareq", s.name))) return;
    try { await api("DELETE", NB + "/shares/" + enc(s.name)); load(); } catch (e) { fail(e); }
  };
  const ok = users.length > 0 && pools.length > 0;
  const close = () => setDlg(null);
  return html`<div class="card"><${Head} title=${t("nas.shares")} label=${admin && ok && t("nas.newshare")} onNew=${() => setDlg({ kind: "new" })} />
    ${sh.length === 0 && html`<p class="muted">${t("nas.noshares")}${admin && !ok ? " " + t("nas.sharesneed") : ""}</p>`}
    ${sh.length > 0 && html`<div class="table-wrap tall"><table class="data plain">
      <thead><tr><th>${t("nas.share")}</th><th>${t("nas.ds.dataset")}</th><th>${t("nas.address")}</th><th>${t("nas.rw")}</th><th>${t("nas.ro")}</th><th>${t("nas.used")}</th><th></th></tr></thead>
      <tbody>${sh.map((s) => html`<tr><td>${s.name}</td><td class="mono small">${s.dataset || (s.pool ? s.pool + "/shares/" + s.name : "")}</td><td class="mono">\\\\${host}\\${s.name}</td><td>${names(s.rw)}${s.managers && s.managers.length ? html`<div class="muted small">${t("nas.manager")}: ${names(s.managers)}</div>` : null}</td><td>${names(s.ro)}</td><td>${gib(s.used)}</td>
        <td><button class="btn" onClick=${() => setDlg({ kind: "folders", share: s })}>${t("nas.folders")}</button>${" "}<button class="btn" onClick=${() => setDlg({ kind: "snaps", share: s })}>${t("nas.snaps")}</button>${" "}
          ${admin && html`<button class="btn" onClick=${() => setDlg({ kind: "edit", share: s })}>${t("nas.edit")}</button>${" "}
            <button class="btn danger" onClick=${() => del(s)}>${t("nas.delete")}</button>`}</td></tr>`)}</tbody></table></div>`}
    ${dlg && dlg.kind === "new" && html`<${ShareForm} shares=${sh} users=${users} pools=${pools} onClose=${close} onDone=${load} />`}
    ${dlg && dlg.kind === "edit" && html`<${ShareForm} share=${dlg.share} users=${users} pools=${pools} onClose=${close} onDone=${load} />`}
    ${dlg && dlg.kind === "snaps" && html`<${Snapshots} share=${dlg.share.name} admin=${admin} onClose=${close} />`}
    ${dlg && dlg.kind === "folders" && html`<${Folders} share=${dlg.share} users=${users} admin=${admin} onClose=${close} />`}
  </div>`;
}

// UserForm: a new user (user undefined), or a new password for one.
function UserForm({ user, onClose, onDone }) {
  const [name, setName] = useState("");
  const [pw, setPw] = useState("");
  const save = async () => {
    if (user) await api("PUT", NB + "/users/" + enc(user.name), { password: pw });
    else await api("POST", NB + "/users", { name: name.trim(), password: pw });
    onDone();
  };
  return html`<${FormDialog} title=${user ? t("nas.passwdof", user.name) : t("nas.newuser")} submit=${user ? t("nas.save") : t("nas.adduser")} onSubmit=${save} onClose=${onClose}>
    ${!user && html`<label>${t("nas.username")}<input type="text" required pattern=${namePattern} placeholder="taro" title=${t("nas.namehint")} value=${name} onInput=${(e) => setName(e.target.value)} /><span class="muted small">${t("nas.namehint")}</span></label>`}
    <label>${t("nas.password")}<input type="password" required autocomplete="new-password" value=${pw} onInput=${(e) => setPw(e.target.value)} /></label>
  <//>`;
}

function Users({ admin, tick, inst, onChange }) {
  const [us, setUs] = useState(null);
  const [all, setAll] = useState(null); // GET …/users: also keelAuth's users and groups
  const [dlg, setDlg] = useState(null); // {kind: "new"} | {kind: "passwd", user}
  const load = () => api("GET", NB + "/users").then((r) => { setUs(r.users || []); setAll(r); }).catch(fail);
  useEffect(() => { load(); }, [tick]);
  if (!us) return html`<div class="stack"><${Spinner} /></div>`;
  const del = async (u) => {
    if (!await ask(t("nas.deluserq", u.name))) return;
    try { await api("DELETE", NB + "/users/" + enc(u.name)); load(); onChange(); } catch (e) { fail(e); }
  };
  const done = () => { load(); onChange(); };
  return html`<div class="stack"><div class="card"><${Head} title=${t("nas.users")} label=${admin && t("nas.newuser")} onNew=${() => setDlg({ kind: "new" })} info=${t("nas.usersnote")} />
    ${us.length > 0 && html`<div class="table-wrap tall"><table class="data plain"><tbody>
      ${us.map((u) => html`<tr><td>${u.name}</td><td>${admin && html`<button class="btn" onClick=${() => setDlg({ kind: "passwd", user: u })}>${t("nas.passwd")}</button>${" "}
        <button class="btn danger" onClick=${() => del(u)}>${t("nas.delete")}</button>`}</td></tr>`)}</tbody></table></div>`}
    ${dlg && html`<${UserForm} user=${dlg.user} onClose=${() => setDlg(null)} onDone=${done} />`}
  </div>
  <${DirUsers} admin=${admin} inst=${inst} all=${all} tick=${tick} onChange=${done} /></div>`;
}

// dirState: a keelAuth user on this keelNAS, in words (keelNAS docs/04 §12: problem, no hash, disabled).
function dirState(u) {
  if (u.problem) return html`<span class="warn">${tr("nas.dir.p." + u.problem, u.problem)}</span>`;
  if (!u.has_password) return html`<span class="warn">${t("nas.dir.nopw")}</span>`;
  if (!u.enabled) return html`<span class="muted">${t("nas.dir.disabled")}</span>`;
  return html`<span class="state running">${t("nas.dir.ok")}</span>`;
}

// DirUsers: users and groups from keelAuth (docs/54 §3.3). keel holds both keys and pushes them:
// /v1/instances/<id>/directory connects, syncs, disconnects; the users come with GET …/users.
function DirUsers({ admin, inst, all, tick, onChange }) {
  const [d, setD] = useState(null);
  const [dlg, setDlg] = useState(null); // "connect" | "disconnect"
  const [k, setK] = useState(0);
  const base = "/v1/instances/" + enc(inst) + "/directory";
  useEffect(() => { api("GET", base).then(setD).catch(fail); }, [tick, k, inst]);
  if (!d || !all) return html`<${Spinner} />`;
  const again = () => { setK(k + 1); onChange(); };
  const syncNow = async () => { try { await api("POST", base + "/sync"); again(); } catch (e) { fail(e); again(); } };
  const auths = d.available || [];
  const synced = all.synced || [];
  const groups = all.groups || [];
  const who = (d.auth_name || d.domain || d.auth) + (d.domain && d.auth_name ? " (" + d.domain + ")" : "");
  return html`<div class="card"><div class="row-head"><h3>${t("nas.dir.title")}</h3><span class="spacer"></span>
      ${admin && html`<span>${d.connected ? html`<button class="btn" onClick=${syncNow}>${t("nas.dir.syncnow")}</button>${" "}
        <button class="btn danger" onClick=${() => setDlg("disconnect")}>${t("nas.dir.disconnect")}</button>`
        : auths.length > 0 && html`<button class="primary" onClick=${() => setDlg("connect")}>${t("nas.dir.connect")}</button>`}</span>`}
      <${Info}>${t(d.connected ? "nas.dir.note" : "nas.dir.what")}<//></div>
    ${!d.connected && html`${auths.length === 0 && html`<p class="muted small">${t("nas.dir.none")} <a href="#/accounts">${t("nas.dir.toaccounts")} →</a></p>`}`}
    ${d.connected && html`<p class="small">${t("nas.dir.from", who)}${d.synced ? html` <span class="muted">· ${t("nas.dir.synced", fmtTime(d.synced))}</span>` : ""}</p>
      ${d.error && html`<p class="error small">${d.error}</p>`}
      ${synced.length === 0 ? html`<p class="muted">${t("nas.dir.nousers")}</p>` : html`<div class="table-wrap tall"><table class="data plain">
        <thead><tr><th>${t("nas.username")}</th><th>${t("nas.dir.display")}</th><th>${t("nas.dir.groups")}</th><th>${t("nas.dir.state")}</th></tr></thead>
        <tbody>${synced.map((u) => html`<tr><td>${u.name}</td><td>${u.display_name}</td><td>${names(u.groups)}</td><td class="small">${dirState(u)}</td></tr>`)}</tbody></table></div>`}
      ${groups.length > 0 && html`<h4>${t("nas.dir.groups")}</h4><div class="table-wrap tall"><table class="data plain">
        <tbody>${groups.map((g) => html`<tr><td>@${g.name}</td><td>${names(g.members)}</td><td class="small">${g.problem ? html`<span class="warn">${tr("nas.dir.p." + g.problem, g.problem)}</span>` : ""}</td></tr>`)}</tbody></table></div>`}`}
    ${dlg === "connect" && html`<${DirConnect} base=${base} auths=${auths} onClose=${() => setDlg(null)} onDone=${again} />`}
    ${dlg === "disconnect" && html`<${DirDisconnect} base=${base} who=${who} onClose=${() => setDlg(null)} onDone=${again} />`}
  </div>`;
}

function DirConnect({ base, auths, onClose, onDone }) {
  const [auth, setAuth] = useState(auths[0].id);
  const go = async () => { await api("PUT", base, { auth }); onDone(); };
  return html`<${FormDialog} title=${t("nas.dir.connect")} submit=${t("nas.dir.connectgo")} onSubmit=${go} onClose=${onClose}>
    <label>keelAuth<select value=${auth} onChange=${(e) => setAuth(e.target.value)}>
      ${auths.map((a) => html`<option value=${a.id}>${[a.name || a.id, a.domain].filter(Boolean).join(" · ")}</option>`)}</select></label>
    <p class="muted small">${t("nas.dir.what")}</p>
  <//>`;
}

function DirDisconnect({ base, who, onClose, onDone }) {
  const [mode, setMode] = useState("keep");
  const go = async () => { await api("DELETE", base + "?users=" + mode); onDone(); };
  return html`<${FormDialog} title=${t("nas.dir.disconnectq", who)} submit=${t("nas.dir.disconnect")} danger=${mode === "delete"} onSubmit=${go} onClose=${onClose}>
    <p>${t("nas.dir.whatbecomes")}</p>
    <label class="check"><input type="radio" name="dirmode" checked=${mode === "keep"} onChange=${() => setMode("keep")} /> ${t("nas.dir.keep")}</label>
    <label class="check"><input type="radio" name="dirmode" checked=${mode === "delete"} onChange=${() => setMode("delete")} /> ${t("nas.dir.delete")}</label>
  <//>`;
}

// Net: keelNAS's LAN NIC (user 2026-10-03: "需要加一个网络配置的tab"). keel reaches keelNAS over
// its internal NIC (keelNAS docs/04 §11), so a wrong LAN setting does not lock it out ("它网络不对
// 我们也应该可以设定得到"); a keelNAS made before the internal NIC has none: then it warns.
function Net({ admin }) {
  const [n, setN] = useState(null);
  const [edit, setEdit] = useState(false);
  const [k, setK] = useState(0);
  const [err, setErr] = useState(null);
  useEffect(() => { api("GET", NB + "/network").then(setN).catch(setErr); }, [k]);
  // an older keelNAS has no network settings (keelnasd answers 404): say so instead of spinning
  if (err) return html`<div class="card"><h3>${t("nas.net")}</h3><p class=${err.status === 404 ? "warn" : "error"}>${err.status === 404 ? t("nas.net.old") : err.message}</p></div>`;
  if (!n) return html`<${Spinner} />`;
  const st = n.settings || { mode: "dhcp" };
  const l = n.lan;
  const row = (key, v) => html`<tr><td class="muted">${t(key)}</td><td>${v || "–"}</td></tr>`;
  return html`<div class="card"><${Head} title=${t("nas.net")} label=${admin && t("nas.edit")} onNew=${() => setEdit(true)} />
    <div class="table-wrap"><table class="data plain"><tbody>
      ${row("nas.net.mode", t(st.mode === "static" ? "nas.net.static" : "nas.net.dhcp"))}
      ${row("nas.net.addr", l && l.addresses.join(", "))}
      ${row("nas.net.gw", n.gateway)}
      ${row("nas.net.dns", (n.dns || []).join(", "))}
      ${row("nas.net.nic", l && html`<span class="mono">${l.name} · ${l.mac}</span> <span class=${"badge" + (l.link ? " ok" : "")}>${t(l.link ? "nas.net.link" : "nas.net.nolink")}</span>`)}
    </tbody></table></div>
    ${n.internal ? html`<p class="muted small">${t("nas.net.internal", (n.internal.addresses || []).join(", "))}</p>`
      : html`<p class="warn small">${t("nas.net.nointernal")}</p>`}
    ${edit && html`<${NetForm} st=${st} onClose=${() => setEdit(false)} onDone=${() => { setEdit(false); toast(t("nas.net.saved")); setTimeout(() => setK((x) => x + 1), 4000); }} />`}
  </div>`;
}

function NetForm({ st, onClose, onDone }) {
  const [mode, setMode] = useState(st.mode || "dhcp");
  const [addr, setAddr] = useState(st.address || "");
  const [gw, setGw] = useState(st.gateway || "");
  const [dns, setDns] = useState((st.dns || []).join(", "));
  const save = async () => {
    const body = mode === "dhcp" ? { mode } : { mode, address: addr.trim(), gateway: gw.trim(), dns: dns.split(/[\s,]+/).filter(Boolean) };
    await api("PUT", NB + "/network", body);
    onDone();
  };
  return html`<${FormDialog} title=${t("nas.net.title")} submit=${t("nas.save")} onSubmit=${save} onClose=${onClose}>
    <div class="actions">${["dhcp", "static"].map((m) => html`<label class="check"><input type="radio" name="nasnet" checked=${mode === m} onChange=${() => setMode(m)} /> ${t("nas.net." + m)}</label>`)}</div>
    ${mode === "static" && html`
      <label>${t("nas.net.addr")}<input type="text" required placeholder="192.168.1.20/24" value=${addr} onInput=${(e) => setAddr(e.target.value)} /></label>
      <label>${t("nas.net.gw")}<input type="text" placeholder="192.168.1.1" value=${gw} onInput=${(e) => setGw(e.target.value)} /></label>
      <label>${t("nas.net.dns")}<input type="text" placeholder="192.168.1.1, 1.1.1.1" value=${dns} onInput=${(e) => setDns(e.target.value)} /></label>`}
    <p class="muted small">${t("nas.net.note")}</p>
  <//>`;
}

function NewLUN({ pools, luns, onClose, onDone }) {
  const [name, setName] = useState(() => freeName("lun", luns, true)); // a free suggestion, lun0, lun1…
  const [size, setSize] = useState(10);
  const [pool, setPool] = useState(pools[0].name);
  const make = async () => { await api("POST", NB + "/luns", { name: name.trim(), pool, size_gib: Number(size) }); onDone(); };
  return html`<${FormDialog} title=${t("nas.newlun")} submit=${t("nas.addlun")} onSubmit=${make} onClose=${onClose}>
    <label>${t("nas.lunname")}<input type="text" required pattern=${namePattern} title=${t("nas.namehint")} value=${name} onInput=${(e) => setName(e.target.value)} /><span class="muted small">${t("nas.namehint")}</span></label>
    <label>${t("nas.size")} (GiB)<input type="number" min="1" required value=${size} onInput=${(e) => setSize(e.target.value)} /></label>
    ${pools.length > 1 && html`<label>${t("nas.storage")}<select value=${pool} onChange=${(e) => setPool(e.target.value)}>
      ${pools.map((p, i) => html`<option value=${p.name}>${t("nas.storagen", i + 1)}</option>`)}</select></label>`}
  <//>`;
}

function LUNs({ admin, tick }) {
  const [ls, setLs] = useState(null);
  const [pools, setPools] = useState([]);
  const [adding, setAdding] = useState(false);
  const load = () => Promise.all([api("GET", NB + "/luns"), api("GET", NB + "/storage")])
    .then(([l, p]) => { setLs(l.luns || []); setPools(p.storage || []); }).catch(fail);
  useEffect(() => { load(); }, [tick]);
  if (!ls) return html`<${Spinner} />`;
  const del = async (l) => {
    if (!await ask(t("nas.dellunq", l.name))) return;
    try { await api("DELETE", NB + "/luns/" + enc(l.name)); load(); } catch (e) { fail(e); }
  };
  return html`<div class="card"><${Head} title="iSCSI" label=${admin && pools.length > 0 && t("nas.newlun")} onNew=${() => setAdding(true)} />
    ${ls.length === 0 && html`<p class="muted">${t("nas.noluns")}</p>`}
    ${ls.length > 0 && html`<div class="table-wrap tall"><table class="data plain">
      <thead><tr><th>LUN</th><th>${t("nas.size")}</th><th>IQN</th><th></th></tr></thead>
      <tbody>${ls.map((l) => html`<tr><td>${l.name}</td><td>${l.size_gib} GiB</td><td class="mono small">${l.iqn}</td>
        <td>${admin && html`<button class="btn danger" onClick=${() => del(l)}>${t("nas.delete")}</button>`}</td></tr>`)}</tbody></table></div>`}
    ${adding && html`<${NewLUN} pools=${pools} luns=${ls} onClose=${() => setAdding(false)} onDone=${load} />`}
  </div>`;
}

const dash = (v) => (v === undefined || v === null || v === "" ? "–" : v);
// disk sizes as the vendor counts them (decimal)
const gb = (b) => (b >= 1e12 ? (b / 1e12).toFixed(b >= 1e13 ? 0 : 1) + " TB" : Math.round(b / 1e9) + " GB");
const healthOf = (d) => (d.health === "passed" ? t("nas.hw.passed") : d.health === "failed" ? t("nas.hw.failed") : t("nas.hw.nosmart"));
const hoursOf = (h) => (h === undefined ? "–" : h >= 24 * 365 ? (h / 24 / 365).toFixed(1) + " y (" + h + " h)" : h + " h");

// DiskHealth: what the disk says about itself in full, in a dialog, like keel's host dialog:
// smartctl -x, and an NVMe disk's SMART/health log page 2 from nvmecontrol (keelnasd nvme_log).
function DiskHealth({ d, path, onClose }) {
  const [x, setX] = useState(null);
  const [err, setErr] = useState("");
  const [tab, setTab] = useState("smart");
  useEffect(() => { api("GET", path + encodeURIComponent(d.name)).then(setX).catch((e) => setErr(e.message)); }, []);
  const tabs = x ? [["smart", "SMART (smartctl -x)", x.smart], ["nvme", t("nas.hw.h.nvmelog"), x.nvme_log]].filter((y) => y[2]) : [];
  const cur = tabs.find((y) => y[0] === tab) || tabs[0];
  return html`<${Dialog} wide=${true} title=${html`${t("nas.hw.h.title", d.name)} <span class="muted small">${[d.model, d.serial].filter(Boolean).join(" · ")}</span>`} onClose=${onClose}>
    ${err && html`<p class="error">${err}</p>`}
    ${!x && !err && html`<${Spinner} small=${true} />`}
    ${x && (x.errors || []).length > 0 && html`<p class="warn small">${x.errors.join("; ")}</p>`}
    ${tabs.length > 1 && html`<div class="row-head"><span class="tabs">${tabs.map((y) => html`<button class=${"tab" + (y === cur ? " on" : "")} onClick=${() => setTab(y[0])}>${y[1]}</button>`)}</span></div>`}
    ${cur && html`<pre class="code">${cur[2]}</pre>`}
  </${Dialog}>`;
}

// Hardware: every disk with what SMART says now (keelnasd GET /v1/hardware, keelNAS docs/04
// section 15), like the host's own hardware page: read when the tab opens and every minute while it is open. An older keelnasd
// has no such route: say so instead of a table.
function Hardware() {
  const [h, setH] = useState(null);
  const [err, setErr] = useState("");
  const [hd, setHd] = useState(null); // the disk whose health dialog is open
  useEffect(() => {
    let on = true;
    const load = () => api("GET", NB + "/hardware").then((r) => { if (on) { setH(r); setErr(""); } }).catch((e) => { if (on) setErr(e.status === 404 ? "old" : e.message); });
    load();
    const iv = setInterval(load, 60000);
    return () => { on = false; clearInterval(iv); };
  }, []);
  if (err === "old") return html`<div class="card"><h3>${t("nas.tab.hardware")}</h3><p class="warn">${t("nas.hw.old")}</p></div>`;
  if (err) return html`<div class="card"><h3>${t("nas.tab.hardware")}</h3><p class="error">${err}</p></div>`;
  if (!h) return html`<${Spinner} />`;
  const healthCell = (d) => html`<td data-l=${t("nas.hw.health")}><button type="button" class=${"badge hbtn" + (d.health === "passed" ? " ok" : "")} title=${t("nas.hw.h.tip")} onClick=${() => setHd(d)}
      style=${d.health === "failed" ? "color:var(--danger);border-color:var(--danger)" : null}>${healthOf(d)}</button>
    ${d.standby ? html` <span class="muted small" title=${t("nas.hw.standbytip")}>${t("nas.hw.standby")}</span>` : ""}
    ${d.reallocated > 0 || d.pending > 0 ? html` <span class="warn small">${t("nas.hw.realloc")} ${d.reallocated || 0}/${d.pending || 0}</span>` : ""}
    ${d.media_errors > 0 ? html` <span class="warn small" title=${t("nas.hw.h.mediatip")}>${t("nas.hw.mediaerr")} ${d.media_errors}</span>` : ""}</td>`;
  // one group per controller, in the order of their first disk; disks without one (virtio) last.
  // An older keelnasd names no controllers: a flat list as before.
  const groups = [];
  for (const d of h.disks || []) {
    const k = d.controller ? d.controller.name : "";
    let g = groups.find((x) => x.k === k);
    if (!g) groups.push(g = { k, c: d.controller, disks: [] });
    g.disks.push(d);
  }
  groups.sort((a, b) => !a.k - !b.k);
  const grouped = groups.some((g) => g.k);
  return html`<div class="card"><div class="row-head"><h3>${t("nas.hw.disks")}</h3><${Info}>${t("nas.hw.what")}<//></div>
    <div class="table-wrap tall"><table class="data ports hw-disks nas-hw"><thead><tr>
      <th>${t("nas.hw.name")}</th><th>${t("nas.hw.model")}</th><th>${t("nas.hw.serial")}</th><th>${t("nas.hw.size")}</th><th>${t("nas.hw.storage")}</th><th>${t("nas.hw.health")}</th><th>${t("nas.hw.temp")}</th><th>${t("nas.hw.hours")}</th><th>${t("nas.hw.wear")}</th>
    </tr></thead><tbody>
    ${groups.map((g) => html`${grouped && html`<tr class="vgroup"><td colspan="9"><b>${g.c ? g.c.name : t("nas.hw.virtual")}</b>
        <span class="muted small"> ${[g.c && g.c.desc, g.c && g.c.pci, t("nas.hw.ndisks", g.disks.length)].filter(Boolean).join(" · ")}</span></td></tr>`}
      ${g.disks.map((d) => html`<tr>
      <td style=${grouped ? "padding-left:22px" : null}><b>${d.name}</b> <span class="muted small">${d.protocol || ""}${d.ssd ? " SSD" : ""}</span></td>
      <td data-l=${t("nas.hw.model")}>${dash(d.model)}${d.firmware ? html` <span class="muted small">${d.firmware}</span>` : ""}</td>
      <td data-l=${t("nas.hw.serial")}>${dash(d.serial)}</td><td data-l=${t("nas.hw.size")}>${gb(d.size)}</td>
      <td data-l=${t("nas.hw.storage")}>${d.system ? html`<span class="muted">${t("nas.hw.system")}</span>` : dash(d.pool)}</td>
      ${healthCell(d)}
      <td data-l=${t("nas.hw.temp")}>${d.temperature > 0 ? d.temperature + " °C" : "–"}</td>
      <td data-l=${t("nas.hw.hours")}>${hoursOf(d.hours)}</td>
      <td data-l=${t("nas.hw.wear")} class=${d.wear >= 90 ? "warn" : ""}>${d.wear !== undefined ? d.wear + "%" : "–"}</td></tr>`)}`)}</tbody></table></div>
    <p class="muted small">${t("nas.hw.readat", fmtTime(h.read_at))}</p>
    ${hd && html`<${DiskHealth} key=${hd.name} d=${hd} path=${NB + "/hardware/disks/"} onClose=${() => setHd(null)} />`}
  </div>`;
}

// Searching a keelMail's archive inside its backups (keelMail docs/09 §4).
const isoDay = (d) => d.toISOString().slice(0, 10);

function ArchiveView({ b, e, onClose }) {
  const [m, setM] = useState(null);
  const path = NB + "/backups/" + encodeURIComponent(b.name) + "/archive/msg/" + encodeURIComponent(e.id) + "?snap=" + encodeURIComponent(e.snap);
  useEffect(() => { api("GET", path).then(setM).catch((x) => { toast(x.message, 8000); onClose(); }); }, []);
  const download = async () => {
    try {
      const r = await api("GET", path + "&eml=1");
      const bin = Uint8Array.from(atob(r.eml), (c) => c.charCodeAt(0));
      const a = document.createElement("a");
      a.href = URL.createObjectURL(new Blob([bin], { type: "message/rfc822" }));
      a.download = e.id + ".eml";
      a.click();
      setTimeout(() => URL.revokeObjectURL(a.href), 5000);
    } catch (x) { toast(x.message, 8000); }
  };
  return html`<${Dialog} title=${e.subject || t("nas.ar.nosubject")} onClose=${onClose} wide=${true}>
    <table class="data plain"><tbody>
      <tr><td class="muted">${t("nas.ar.time")}</td><td>${fmtTime(e.t)} · ${t("nas.ar.dir." + e.dir)}</td></tr>
      <tr><td class="muted">${t("nas.ar.from")}</td><td>${e.from}</td></tr>
      <tr><td class="muted">${t("nas.ar.to")}</td><td>${e.to.join(", ")}</td></tr>
      ${(e.att || []).length > 0 && html`<tr><td class="muted">${t("nas.ar.att")}</td><td>${e.att.join(", ")}</td></tr>`}
      <tr><td class="muted">${t("nas.ar.copy")}</td><td class="mono small">${e.snap}</td></tr>
    </tbody></table>
    ${!m ? html`<${Spinner} />` : html`<pre style="white-space:pre-wrap;word-break:break-word;max-height:50vh;overflow:auto;padding:10px;border:1px solid var(--line)">${m.text}</pre>`}
    <p><button class="btn" onClick=${download}>${t("nas.ar.eml")}</button></p>
  <//>`;
}

function ArchiveAudit({ onClose }) {
  const [l, setL] = useState(null);
  useEffect(() => { api("GET", NB + "/backups/archive-audit").then((r) => setL(r.audit)).catch((x) => toast(x.message, 8000)); }, []);
  const what = (a) => (typeof a.what === "string" ? a.what : Object.entries(a.what || {}).filter(([, v]) => v && v[0]).map(([k, v]) => k + "=" + v[0]).join(" "));
  return html`<${Dialog} title=${t("nas.ar.audit")} onClose=${onClose} wide=${true}>
    ${!l ? html`<${Spinner} />` : l.length === 0 ? html`<p class="muted">${t("nas.ar.noaudit")}</p>` : html`<div class="table-wrap tall"><table class="data plain">
      <tbody>${l.map((a) => html`<tr><td>${fmtTime(a.t)}</td><td>${a.who || a.via}</td><td>${t("nas.ar.a." + a.action)}</td><td class="mono small">${what(a)}</td></tr>`)}</tbody></table></div>`}
  <//>`;
}

function ArchiveSearch({ b, onClose }) {
  const now = new Date();
  const [q, setQ] = useState({ from: isoDay(new Date(now - 90 * 864e5)), to: isoDay(now), dir: "", sender: "", rcpt: "", subject: "", att: "", body: "" });
  const [r, setR] = useState(null);
  const [busy, setBusy] = useState(false);
  const [open, setOpen] = useState(null);
  const [audit, setAudit] = useState(false);
  const go = async (ev) => {
    if (ev) ev.preventDefault();
    setBusy(true);
    const p = new URLSearchParams(Object.entries(q).filter(([, v]) => v !== ""));
    try { setR(await api("GET", NB + "/backups/" + encodeURIComponent(b.name) + "/archive?" + p)); } catch (x) { toast(x.message, 8000); }
    setBusy(false);
  };
  const f = (k) => ({ value: q[k], onInput: (e) => setQ({ ...q, [k]: e.target.value }) });
  return html`<${Dialog} title=${t("nas.ar.title", b.label_now || b.label || b.name)} onClose=${onClose} wide=${true}>
    <p class="muted small">${t("nas.ar.what")} <a href="javascript:void 0" onClick=${() => setAudit(true)}>${t("nas.ar.audit")}</a></p>
    <form onSubmit=${go} style="display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:6px 10px;align-items:end">
      <label>${t("nas.ar.from_d")}<input type="date" required ...${f("from")} /></label>
      <label>${t("nas.ar.to_d")}<input type="date" required ...${f("to")} /></label>
      <label>${t("nas.ar.dir")}<select value=${q.dir} onChange=${(e) => setQ({ ...q, dir: e.target.value })}>${["", "in", "out", "internal"].map((d) => html`<option value=${d}>${t("nas.ar.dir." + (d || "all"))}</option>`)}</select></label>
      <label>${t("nas.ar.from")}<input type="text" ...${f("sender")} /></label>
      <label>${t("nas.ar.to")}<input type="text" ...${f("rcpt")} /></label>
      <label>${t("nas.ar.subject")}<input type="text" ...${f("subject")} /></label>
      <label>${t("nas.ar.att")}<input type="text" placeholder="*" ...${f("att")} /></label>
      <label>${t("nas.ar.body")}<input type="text" ...${f("body")} /></label>
      <div><button class="primary" type="submit" disabled=${busy}>${busy ? t("nas.ar.searching") : t("nas.ar.search")}</button></div>
    </form>
    ${r && html`<p><b>${t("nas.ar.n", r.total)}</b>${r.total > r.results.length ? html` <span class="warn small">${t("nas.ar.more", r.results.length)}</span>` : ""}
      ${r.missing.length > 0 && html`<br /><span class="muted small">${t("nas.ar.missing", r.missing[0], r.missing[r.missing.length - 1])}</span>`}</p>
      ${r.results.length > 0 && html`<div class="table-wrap tall"><table class="data plain">
        <thead><tr><th>${t("nas.ar.time")}</th><th>${t("nas.ar.dir")}</th><th>${t("nas.ar.from")}</th><th>${t("nas.ar.to")}</th><th>${t("nas.ar.subject")}</th></tr></thead>
        <tbody>${r.results.map((e) => html`<tr style="cursor:pointer" onClick=${() => setOpen(e)}><td>${fmtTime(e.t)}</td><td>${t("nas.ar.dir." + e.dir)}</td>
          <td class="small">${e.from}</td><td class="small">${e.to.slice(0, 2).join(", ")}${e.to.length > 2 ? " +" + (e.to.length - 2) : ""}</td>
          <td>${e.subject || t("nas.ar.nosubject")}${(e.att || []).length ? " 📎" : ""}</td></tr>`)}</tbody></table></div>`}`}
    ${open && html`<${ArchiveView} b=${b} e=${open} onClose=${() => setOpen(null)} />`}
    ${audit && html`<${ArchiveAudit} onClose=${() => setAudit(false)} />`}
  <//>`;
}

// Fetching backups of other keel systems (keelMail, keelAuth …; keelNAS docs/08): the sources,
// fetch now, pause, the hour, how long keelNAS keeps them, the encryption key.
const hourUTC = (h) => String(h).padStart(2, "0") + ":00 UTC";

function BackupAdd({ pools, defHour, onClose, onDone }) {
  const [tok, setTok] = useState("");
  const [pool, setPool] = useState(pools[0] ? pools[0].name : "");
  const [hour, setHour] = useState(String(defHour));
  const save = async () => {
    const r = await api("POST", NB + "/backups", { token: tok.trim(), pool, hour: Number(hour) });
    toast(t("nas.bk.added", r.label || r.name));
    onDone();
  };
  return html`<${FormDialog} title=${t("nas.bk.add")} submit=${t("nas.bk.addgo")} onSubmit=${save} onClose=${onClose}>
    <label>${t("nas.bk.token")}<textarea rows="3" required spellcheck="false" class="mono" value=${tok} onInput=${(e) => setTok(e.target.value)}></textarea></label>
    <p class="muted small">${t("nas.bk.tokenhint")}</p>
    ${pools.length > 1 && html`<label>${t("nas.bk.pool")}<select value=${pool} onChange=${(e) => setPool(e.target.value)}>${pools.map((p, i) => html`<option value=${p.name}>${t("nas.storagen", i + 1)}</option>`)}</select></label>`}
    <label>${t("nas.bk.hour")}<select value=${hour} onChange=${(e) => setHour(e.target.value)}>${Array.from({ length: 24 }, (_, h) => html`<option value=${h}>${hourUTC(h)}</option>`)}</select></label>
    <p class="muted small">${t("nas.bk.hourhint")}</p>
  <//>`;
}

function BackupEdit({ b, onClose, onDone }) {
  const [hour, setHour] = useState(String(b.hour));
  const [tok, setTok] = useState("");
  const save = async () => {
    const body = { hour: Number(hour) };
    if (tok.trim()) body.token = tok.trim();
    await api("PUT", NB + "/backups/" + encodeURIComponent(b.name), body);
    onDone();
  };
  return html`<${FormDialog} title=${b.label || b.name} submit=${t("nas.save")} onSubmit=${save} onClose=${onClose}>
    <label>${t("nas.bk.hour")}<select value=${hour} onChange=${(e) => setHour(e.target.value)}>${Array.from({ length: 24 }, (_, h) => html`<option value=${h}>${hourUTC(h)}</option>`)}</select></label>
    <label>${t("nas.bk.newtoken")}<textarea rows="3" spellcheck="false" class="mono" value=${tok} onInput=${(e) => setTok(e.target.value)}></textarea></label>
    <p class="muted small">${t("nas.bk.newtokenhint")}</p>
  <//>`;
}

function BackupKeepForm({ keep, onClose, onDone }) {
  const [d, setD] = useState(String(keep.daily));
  const [m, setM] = useState(String(keep.monthly));
  const save = async () => { await api("PUT", NB + "/backups/keep", { daily: Number(d), monthly: Number(m) }); onDone(); };
  return html`<${FormDialog} title=${t("nas.bk.keep")} submit=${t("nas.save")} onSubmit=${save} onClose=${onClose}>
    <label>${t("nas.bk.keepd")}<input type="number" min="1" max="1000" required value=${d} onInput=${(e) => setD(e.target.value)} /></label>
    <label>${t("nas.bk.keepm")}<input type="number" min="0" max="1000" required value=${m} onInput=${(e) => setM(e.target.value)} /></label>
  <//>`;
}

function Backups({ admin, tick }) {
  const [d, setD] = useState(null);
  const [err, setErr] = useState(null);
  const [dlg, setDlg] = useState(null);
  const [pools, setPools] = useState([]);
  const [k, setK] = useState(0);
  const reload = () => setK((x) => x + 1);
  useEffect(() => {
    api("GET", NB + "/backups").then((r) => { setD(r); setErr(null); }).catch(setErr);
    api("GET", NB + "/status").then((r) => setPools(r.pools || [])).catch(() => {});
  }, [k, tick]);
  // while a fetch runs: read again every three seconds (progress, then the result)
  const busy = d && d.sources.some((b) => b.running);
  useEffect(() => {
    if (!busy) return undefined;
    const id = setInterval(reload, 3000);
    return () => clearInterval(id);
  }, [busy]);
  if (err) return html`<div class="card"><h3>${t("nas.tab.backups")}</h3><p class=${err.status === 404 ? "warn" : "error"}>${err.status === 404 ? t("nas.bk.old") : err.message}</p></div>`;
  if (!d) return html`<${Spinner} />`;
  const act = async (fn) => { try { await fn(); reload(); } catch (e) { toast(e.message, 8000); } };
  const fetchNow = (b) => act(async () => { await api("POST", NB + "/backups/" + encodeURIComponent(b.name) + "/fetch"); toast(t("nas.bk.fetching")); });
  const pause = (b) => act(() => api("PUT", NB + "/backups/" + encodeURIComponent(b.name), { paused: !b.paused }));
  const del = async (b) => {
    if (!await ask(t("nas.bk.delq", b.label || b.name))) return;
    const data = await ask(t("nas.bk.deldataq", gib(b.used)));
    act(() => api("DELETE", NB + "/backups/" + encodeURIComponent(b.name) + (data ? "?data=1" : "")));
  };
  const showKey = async () => {
    try { const r = await api("GET", NB + "/backups/key"); setDlg({ kind: "key", key: r.key }); } catch (e) { toast(e.message, 8000); }
  };
  const state = (b) => b.running ? html`<span class="badge">${t("nas.bk.running")}</span>${b.moved > 0 ? html` <span class="muted small">${(b.moved / 1048576).toFixed(1)} MiB</span>` : ""}`
    : b.paused ? html`<span class="badge">${t("nas.bk.paused")}</span>`
    : b.error ? html`<span class="badge" style="color:var(--danger);border-color:var(--danger)" title=${b.error}>${t("nas.bk.failed")}</span>`
    : b.last_at ? html`<span class="badge ok">${t("nas.bk.ok")}</span>` : html`<span class="badge">${t("nas.bk.never")}</span>`;
  return html`<div class="card"><${Head} title=${t("nas.tab.backups")} label=${admin && pools.length > 0 && t("nas.bk.add")} onNew=${() => setDlg({ kind: "add" })} />
    <p class="muted small">${t("nas.bk.what")}</p>
    ${pools.length === 0 && html`<p class="warn">${t("nas.bk.nopool")}</p>`}
    ${d.sources.length === 0 ? html`<p class="muted">${t("nas.bk.none")}</p>` : html`<div class="table-wrap"><table class="data plain">
      <thead><tr><th>${t("nas.bk.source")}</th><th>${t("nas.bk.state")}</th><th>${t("nas.bk.last")}</th><th>${t("nas.bk.copies")}</th><th>${t("nas.bk.size")}</th><th>${t("nas.bk.hour")}</th><th></th></tr></thead>
      <tbody>${d.sources.map((b) => html`<tr>
        <td><b>${b.label_now || b.label || b.name}</b> <span class="muted small">${b.kind} · ${b.host}</span></td>
        <td>${state(b)}${b.error && html`<div class="error small">${b.error}</div>`}</td>
        <td>${b.last_at ? html`${fmtTime(b.last_at)} <span class="muted small mono">${b.last_snap}</span>` : "–"}</td>
        <td>${b.snapshots.length}${b.snapshots.length > 0 && html` <span class="muted small">${t("nas.bk.oldest", fmtTime(b.snapshots[0].time))}</span>`}</td>
        <td>${gib(b.used)}</td><td>${hourUTC(b.hour)}</td>
        <td class="nowrap">${admin && html`<button class="btn" disabled=${b.running} onClick=${() => fetchNow(b)}>${t("nas.bk.now")}</button>
          <button class="btn" onClick=${() => pause(b)}>${t(b.paused ? "nas.bk.resume" : "nas.bk.pause")}</button>
          ${b.kind === "keelmail" && b.snapshots.length > 0 && html`<button class="btn" onClick=${() => setDlg({ kind: "archive", b })}>${t("nas.ar.open")}</button>`}
          <button class="btn" onClick=${() => setDlg({ kind: "edit", b })}>${t("nas.edit")}</button>
          <button class="btn" disabled=${b.running} onClick=${() => del(b)}>${t("nas.bk.delete")}</button>`}</td></tr>`)}</tbody></table></div>`}
    <p class="muted small">${t("nas.bk.keepnow", d.keep.daily, d.keep.monthly)}
      ${admin && html` <a href="javascript:void 0" onClick=${() => setDlg({ kind: "keep" })}>${t("nas.edit")}</a>`}</p>
    <p class="muted small">${t(d.key_set ? "nas.bk.keyset" : "nas.bk.keynone")}
      ${admin && d.key_set && html` <a href="javascript:void 0" onClick=${showKey}>${t("nas.bk.showkey")}</a>`}</p>
    ${dlg && dlg.kind === "add" && html`<${BackupAdd} pools=${pools} defHour=${d.default_hour} onClose=${() => setDlg(null)} onDone=${() => { setDlg(null); reload(); }} />`}
    ${dlg && dlg.kind === "edit" && html`<${BackupEdit} b=${dlg.b} onClose=${() => setDlg(null)} onDone=${() => { setDlg(null); reload(); }} />`}
    ${dlg && dlg.kind === "archive" && html`<${ArchiveSearch} b=${dlg.b} onClose=${() => setDlg(null)} />`}
    ${dlg && dlg.kind === "keep" && html`<${BackupKeepForm} keep=${d.keep} onClose=${() => setDlg(null)} onDone=${() => { setDlg(null); reload(); }} />`}
    ${dlg && dlg.kind === "key" && html`<${Dialog} title=${t("nas.bk.key")} onClose=${() => setDlg(null)}>
      <p class="mono" style="word-break:break-all;user-select:all">${dlg.key}</p><p class="warn small">${t("nas.bk.keynote")}</p><//>`}
  </div>`;
}

// The sub-tabs: one thing per page instead of one long page.
export const tabs = ["overview", "storage", "hardware", "shares", "users", "iscsi", "network", "backups"];
export const tabLabel = (k) => t("nas.tab." + k);

export default function Page({ base, tab, here, admin, info, st, tick, bump }) {
  NB = base;
  const errs = st && st.last_apply && st.last_apply.errors ? st.last_apply.errors : [];
  const host = (info.addresses && info.addresses[0]) || info.hostname;
  const pools = (st && st.pools) || [];
  return html`<div class="stack" key=${info.id}>
    ${tab === "overview" && html`
      <div class="card"><h3>${info.name || "keelNAS"} <span class="muted small">${info.hostname} · ${host} · ${info.version}</span></h3>
        ${st && html`<div class="small">
          <span class=${"badge" + (st.services && st.services.smbd ? " ok" : "")}>SMB</span>${" "}
          <span class=${"badge" + (st.services && st.services.ctld ? " ok" : "")}>iSCSI</span>
          <span class="muted"> ${t("nas.mem", gib(st.memory.free), gib(st.memory.total), gib(st.memory.arc))}</span></div>`}
        ${errs.length > 0 && html`<p class="error small">${errs.join(" · ")}</p>`}</div>
      <${Alerts} tick=${tick} />
      <div class="card"><h3>${t("nas.storage")}</h3>
        ${pools.length === 0 && html`<p class="muted">${t("nas.nostorage")} <a href=${here + "storage"}>${t("nas.tab.storage")} →</a></p>`}
        ${pools.map((p, i) => html`<div style="margin-bottom:10px"><b>${t("nas.storagen", i + 1)}</b> <${Health} h=${p.health} /> <${Protected} p=${p} />
          <span class="muted small"> ${gib(p.used)} / ${gib(p.size)}</span><${Bar} pct=${p.size ? (100 * p.used) / p.size : 0} /></div>`)}</div>`}
    ${tab === "storage" && html`<${Storage} admin=${admin} onChange=${bump} />`}
    ${tab === "hardware" && html`<${Hardware} />`}
    ${tab === "shares" && html`<${Shares} admin=${admin} host=${host} tick=${tick} />`}
    ${tab === "users" && html`<${Users} admin=${admin} tick=${tick} inst=${info.id} onChange=${bump} />`}
    ${tab === "iscsi" && html`<${LUNs} admin=${admin} tick=${tick} />`}
    ${tab === "network" && html`<${Net} admin=${admin} />`}
    ${tab === "backups" && html`<${Backups} admin=${admin} tick=${tick} />`}
  </div>`;
}
