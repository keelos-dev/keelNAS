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
  "nas.keep": "Automatic snapshots kept",
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
  "nas.keep": "自动快照保留多少个",
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
  "nas.keep": "自動スナップショットの保存数",
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
const Protected = ({ p }) => html`<span class=${"badge" + (p.protected ? " ok" : "")}>${p.protected ? t("nas.protected") : t("nas.unprotected")}</span>`;
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
  useEffect(() => {
    if (!running) return undefined;
    const h = setTimeout(load, 2000);
    return () => clearTimeout(h);
  }, [trees]);
  if (!st) return html`<${Spinner} />`;
  const free = disks.filter(isFree);
  const inStorage = new Set(st.flatMap((p) => (p.disks || []).map((d) => d.name)));
  const busy = disks.filter((d) => !isFree(d) && !d.system && !inStorage.has(d.name));
  const failing = (n) => disks.some((d) => d.name === n && d.smart === "failing");
  const done = () => { load(); onChange(); };
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
        ${admin && !p.protected && (p.disks || []).length === 1 && free.length > 0 && html` <button class="btn" onClick=${() => setDlg({ kind: "protect", pool: p, label: t("nas.storagen", i + 1) })}>${t("nas.protectbtn")}</button>`}</div>
      <${Bar} pct=${p.size ? (100 * p.used) / p.size : 0} />
      <div class="small muted">${(p.disks || []).map((d, j) => html`${j ? ", " : ""}${d.name}${d.state !== "ONLINE" ? " (" + d.state + ")" : ""}${failing(d.name) ? html` <span class="error">${t("nas.smartbad")}</span>` : null}`)}</div>
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
      ${busy.map((d) => html`<div class="small">${diskText(d)} — <span class="muted">${busyText(d)}</span></div>`)}`}
    ${dlg && dlg.kind === "new" && html`<${NewStorage} free=${free} onClose=${() => setDlg(null)} onDone=${done} />`}
    ${dlg && dlg.kind === "import" && html`<${ImportPool} pool=${dlg.pool} onClose=${() => setDlg(null)} onDone=${done} />`}
    ${dlg && dlg.kind === "protect" && html`<${AddProtection} pool=${dlg.pool} label=${dlg.label} free=${free} onClose=${() => setDlg(null)} onDone=${done} />`}
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
const jobText = (j) => (j.state === "counting" ? t("nas.ds.jobcount")
  : j.state === "running" ? t("nas.ds.jobrun", j.files, j.total)
  : j.state === "failed" ? t("nas.ds.jobfailed", j.error) : "");

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
function Access({ users, value, onChange }) {
  return users.map((u) => html`<label>${u.label || u.name}
    <select value=${value[u.name] || ""} onChange=${(e) => { const v = e.target.value; onChange((a) => ({ ...a, [u.name]: v })); }}>
      <option value="">${t("nas.noaccess")}</option><option value="rw">${t("nas.rw")}</option><option value="ro">${t("nas.ro")}</option>
    </select></label>`);
}
const split = (acc) => ({ rw: Object.keys(acc).filter((u) => acc[u] === "rw"), ro: Object.keys(acc).filter((u) => acc[u] === "ro") });
const join = (s) => Object.fromEntries([...(s.rw || []).map((u) => [u, "rw"]), ...(s.ro || []).map((u) => [u, "ro"])]);
// accessList: who can be named in a share (GET …/users): local users, then keelAuth's users and
// groups that keelNAS made (docs/54 §3.3).
const accessList = (r) => [
  ...(r.users || []).map((u) => ({ name: u.name })),
  ...(r.synced || []).filter((u) => !u.problem).map((u) => ({ name: u.name, label: t("nas.dir.userlabel", u.name) })),
  ...(r.groups || []).filter((g) => !g.problem).map((g) => ({ name: "@" + g.name, label: t("nas.dir.grouplabel", g.name) })),
];
const some = (acc) => split(acc).rw.length + split(acc).ro.length > 0;

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
  const save = async () => {
    if (share) await api("PUT", NB + "/shares/" + enc(share.name), split(acc));
    else if (dataset) await api("POST", NB + "/shares", { dataset: dataset.name, name: name.trim(), ...split(acc) });
    else await api("POST", NB + "/shares", { name: name.trim(), pool, ...split(acc) });
    onDone();
  };
  return html`<${FormDialog} title=${share ? t("nas.editshare", share.name) : dataset ? t("nas.ds.sharetitle", dataset.name) : t("nas.newshare")}
      submit=${share ? t("nas.save") : t("nas.addshare")} disabled=${!some(acc)} onSubmit=${save} onClose=${onClose}>
    ${!share && html`<label>${t("nas.sharename")}<input type="text" required pattern=${namePattern} title=${t("nas.namehint")} value=${name} onInput=${(e) => setName(e.target.value)} /><span class="muted small">${t("nas.namehint")}</span></label>`}
    ${!share && !dataset && pools.length > 1 && html`<label>${t("nas.storage")}<select value=${pool} onChange=${(e) => setPool(e.target.value)}>
      ${pools.map((p, i) => html`<option value=${p.name}>${t("nas.storagen", i + 1)}</option>`)}</select></label>`}
    <${Access} users=${users} value=${acc} onChange=${setAcc} />
    <p class="muted small">${t("nas.sharenote")}</p>
  <//>`;
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
      <tbody>${sh.map((s) => html`<tr><td>${s.name}</td><td class="mono small">${s.dataset || (s.pool ? s.pool + "/shares/" + s.name : "")}</td><td class="mono">\\\\${host}\\${s.name}</td><td>${names(s.rw)}</td><td>${names(s.ro)}</td><td>${gib(s.used)}</td>
        <td><button class="btn" onClick=${() => setDlg({ kind: "snaps", share: s })}>${t("nas.snaps")}</button>${" "}
          ${admin && html`<button class="btn" onClick=${() => setDlg({ kind: "edit", share: s })}>${t("nas.edit")}</button>${" "}
            <button class="btn danger" onClick=${() => del(s)}>${t("nas.delete")}</button>`}</td></tr>`)}</tbody></table></div>`}
    ${dlg && dlg.kind === "new" && html`<${ShareForm} shares=${sh} users=${users} pools=${pools} onClose=${close} onDone=${load} />`}
    ${dlg && dlg.kind === "edit" && html`<${ShareForm} share=${dlg.share} users=${users} pools=${pools} onClose=${close} onDone=${load} />`}
    ${dlg && dlg.kind === "snaps" && html`<${Snapshots} share=${dlg.share.name} admin=${admin} onClose=${close} />`}
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

// DiskHealth: what the disk says about itself in full (smartctl -x), in a dialog.
function DiskHealth({ d, path, onClose }) {
  const [x, setX] = useState(null);
  const [err, setErr] = useState("");
  useEffect(() => { api("GET", path + encodeURIComponent(d.name)).then(setX).catch((e) => setErr(e.message)); }, []);
  return html`<${Dialog} wide=${true} title=${html`${t("nas.hw.h.title", d.name)} <span class="muted small">${[d.model, d.serial].filter(Boolean).join(" · ")}</span>`} onClose=${onClose}>
    ${err && html`<p class="error">${err}</p>`}
    ${!x && !err && html`<${Spinner} small=${true} />`}
    ${x && html`<pre class="code">${x.smart}</pre>`}
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

// The sub-tabs: one thing per page instead of one long page.
export const tabs = ["overview", "storage", "hardware", "shares", "users", "iscsi", "network"];
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
  </div>`;
}
