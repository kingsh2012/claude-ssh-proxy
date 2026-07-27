# 兼容旧设备(弱加密算法)方案

## 背景

部分老旧交换机/网络设备的 SSH 服务只支持过时的算法(如 cipher: `aes128-cbc`、
`3des-cbc`;kex: `diffie-hellman-group1-sha1`、`diffie-hellman-group14-sha1`;
host key: `ssh-rsa`、`ssh-dss`),而 `golang.org/x/crypto/ssh` 默认只启用安全算法集,
导致握手报 `no common algorithm` 之类的错误。

当前(2026-07-27)在 `proxy.go` 的 `dialUpstreamTimeout` 里做了一个**全局**兜底:
在默认 cipher 列表后追加了 `aes128-cbc`、`3des-cbc`。这样能让老设备连上,但代价是
*所有*目标服务器在协商时都会被动接受弱算法,削弱了对能力正常的设备的安全性。

## 更好的方案:按服务器开关

不做全局放开,而是给每台服务器加一个"兼容旧设备(弱加密)"开关,只有勾选的服务器
才在协商时带上这些不安全算法,其余服务器维持严格的现代算法白名单。

### 需要改的地方

1. **数据库 / `ServerRecord`**(`store.go`)
   - 加字段:`LegacyAlgorithms bool `json:"legacy_algorithms"`` (`servers` 表加一列,
     默认 0/false)
   - `UpsertServer` 等增删改查逻辑同步支持这个字段的读写
   - 加一条数据库迁移(参考现有 migration 写法)

2. **连接逻辑**(`proxy.go` 的 `dialUpstreamTimeout`)
   - 去掉现在无条件追加弱算法的写法
   - 改为:仅当 `server.LegacyAlgorithms == true` 时,才在 `ssh.Config` 里追加:
     - Ciphers: `aes128-cbc`、`3des-cbc`
     - KeyExchanges: `diffie-hellman-group1-sha1`、`diffie-hellman-group14-sha1`
     - HostKeyAlgorithms: `ssh-rsa`、`ssh-dss`
   - 未勾选的服务器,`ssh.Config` 保持 x/crypto/ssh 的默认(安全)算法集

3. **API**(`api.go`)
   - 服务器的创建/更新接口(UpsertServer 相关 handler)透传 `legacy_algorithms` 字段

4. **前端**(`webui/`)
   - 服务器编辑表单加一个"兼容旧设备(弱加密算法)"复选框,附带提示文案说明
     这会降低该服务器连接的安全性,仅在确认老设备无法支持现代算法时勾选
   - 服务器列表可以加个小标记,提示哪些服务器开着这个兼容模式,方便审计

### 已知限制

- `golang.org/x/crypto/ssh` 没有实现单 DES(`des-cbc`)算法,这是库本身的限制,
  不是兼容开关能解决的。如果目标设备的 SSH 服务端只会 `des-cbc`(没有 `3des-cbc`
  /`aes128-cbc` 可选),这条路走不通,需要在设备侧升级或开启更高算法支持。

## 现状(已实现,2026-07-27)

已按上面的方案落地:

- `store.go`:`servers` 表加了 `legacy_algorithms INTEGER NOT NULL DEFAULT 0` 列
  (直接写进 `CREATE TABLE IF NOT EXISTS`,沿用本项目"不用 ALTER TABLE 兼容旧库,
  改表就删库重建"的既有约定,见 commit `d48a4b3`);`ServerRecord` 加了
  `LegacyAlgorithms bool` 字段;`UpsertServer`/`scanServer` 同步读写这个字段
- `proxy.go` 的 `dialUpstreamTimeout`:只有 `server.LegacyAlgorithms == true` 时才在
  `ssh.SupportedAlgorithms()` 的默认算法后面追加 `ssh.InsecureAlgorithms()` 里的
  Ciphers、KeyExchanges、HostKeys(涵盖 CBC/3DES、老 KEX、ssh-dss 等),未勾选的
  服务器完全不受影响
- 前端(`webui/src/api.ts`、`ServersPage.tsx`):`ServerRecord` 加 `legacy_algorithms`
  字段,编辑服务器的弹窗里加了"兼容旧设备"复选框,并提示会降低该服务器连接的安全性

`des-cbc`(单 DES)仍然无法解决——`golang.org/x/crypto/ssh` 库本身没有实现,
只能在设备侧升级。
