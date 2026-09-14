# Windows Agent 主动接入

Windows 主动通过 WSS 连接跳板机。LLM 仍使用 SSH 登录跳板机，由跳板机把命令交给 Agent 的 PowerShell 执行。目标机器不需要 OpenSSH、NATS 或入站端口。

```text
LLM --SSH--> ops-ssh-proxy <--WSS 443-- Windows Agent
```

## 支持范围

- Windows 10 / Windows Server 2016 及以上，使用系统 Windows PowerShell 5.1。当前提供 amd64 构建。
- 支持 SSH `exec`：单条命令或包含换行的完整 PowerShell 脚本。每次任务是独立进程，工作目录和变量不跨任务保留。
- 可以列目录、读取文件、搜索日志、调用 ES/Kibana HTTP 接口。文件权限取决于 Agent 运行账号。
- 返回 UTF-8 标准输出、错误输出、退出码，并接入现有审计。
- 每台 Agent 同时执行一个任务；忙时返回退出码 125，不排队、不自动重试。
- 命令上限 64 KiB，输出上限 8 MiB，任务上限 5 分钟；审计沿用现有 64 KiB 截断规则。
- 暂不支持交互 shell、PTY、SSH stdin、SFTP、SCP、端口转发、远程桌面或 Windows 服务安装。
- Agent 是前台程序；关闭终端、按 Ctrl+C 或结束 Agent 会结束连接和当前任务。

## 1. 编译

在项目根目录构建服务端，方法见 [README](../README.md)。构建 Windows Agent：

```powershell
go build -trimpath -o dist/ops-ssh-agent.exe ./cmd/windows-agent
```

在 Linux 构建 Windows Agent：

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o dist/ops-ssh-agent.exe ./cmd/windows-agent
```

## 2. 配置中间服务器 HTTPS

现有 Web 服务仍默认监听 `127.0.0.1:8080`。在 Nginx 的 HTTPS `server` 中加入以下路由，证书应受目标 Windows 信任：

```nginx
location = /agent {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_read_timeout 90s;
    proxy_send_timeout 90s;
    proxy_buffering off;
}
```

保留原来的后台 `/` 反向代理。目标 Windows 需要能解析域名并出站访问 TCP 443；LLM 继续访问跳板机现有 SSH 端口。Agent 校验证书，不提供跳过证书校验的选项。

## 3. 服务设置中的自注册 Token

交互更新：2026-09-14。

1. 打开后台 **服务设置 → 自注册 Token**。
2. 在公网连接地址输入框只填写 Windows 可访问、已配置可信 HTTPS 证书的主域名，例如 `https://proxy.example.com`，点击 **保存地址**。
3. 点击只读输入框旁的 **随机生成密钥**，再点击 **保存**。生成只产生草稿，保存成功后生效；点击输入框全选，复制完整 Token 给 Agent 使用。多台 Windows 共用此密钥。
4. Token 在服务设置中可再次查看和复制；完整 Token 加密保存在数据库中，认证使用 SHA-256 摘要。数据库必须连同 `.db.key` 一起备份。

仅传 `-token` 即可连接，因为 Token 包含服务器地址和随机密钥。后台 HTTP 地址不等于可用的 WSS 地址，需要先配置 HTTPS。修改地址并保存后，Token 中的地址同步更新，随机密钥不变；后续启动 Agent 使用更新后的完整 Token。域名不写死在 exe 中。Agent 的 `-server https://proxy.example.com` 及 HTTPS 地址 Token 自动转换为 `wss://proxy.example.com/agent`；旧 WSS 地址和 Token 继续可用。后台生成的 Token 保留 WSS 格式，兼容旧 Agent。页面只显示 HTTPS 主域名，`/agent` 路由在页面说明中标注。已有主机访问权限不会被覆盖。

## 4. 在 Windows 启动

```powershell
.\ops-ssh-agent.exe -token '服务设置中的自注册Token'
```

可用 `-hostname` 直接指定代理登录名：

```powershell
.\ops-ssh-agent.exe -token '服务设置中的自注册Token' -hostname 'es-windows-01'
```

- 不传 `-hostname` 时使用 Windows 主机名。名称支持字母、数字、点、短横线和下划线，以字母或数字开头，最长 253 字符。
- 同一个 Token 支持多台主机。相同名称重连复用原记录，名称按大小写不敏感匹配。此密钥的持有者可以注册及连接对应名称的路由，名称不是独立设备身份证明。
- 两台机器应使用不同代理登录名。同名并发连接会拒绝后来的连接；与既有 SSH 主机或旧版 Agent 重名也拒绝，不覆盖、不自动加后缀。
- 禁用主机会阻止连接。删除主机后保留名称占用记录，Agent 不会因自动重连恢复被删除的主机；重新接入使用新的 `-hostname`。
- **保存新密钥** 后才会断开使用旧密钥的 Agent，旧 Token 无法注册或重连；仅点击随机生成不会影响当前密钥。轮换后用新 Token 和原名称启动，继续使用原主机记录与访问权限。
- 新主机注册不绑定任何客户端凭证，也不继承历史默认授权。管理员在服务器列表中编辑主机、关联客户端凭证后，才允许 SSH 代理访问；重连不会清除已有的手动授权。
- 前台运行，关闭终端或按 Ctrl+C 停止；断线每 5 秒重连，不重放命令。
- Token 不应写入聊天、工单或 Git；命令参数可能保留在终端历史与本机进程参数中。

需要覆盖连接地址时可增加 `-server 'wss://proxy.example.com/agent'`。已有旧版独立 Token 和 TOML 配置仍可使用；`-config` 与 `-token`、`-server` 不能混用，`-hostname` 可覆盖 TOML 中的 `hostname`。网页已移除手动创建 Agent、生成 TOML 配置及逐台申请 Token 的入口。

## 5. LLM 调用与文件读取

以下示例在本地 PowerShell 中执行，SSH 登录凭证沿用已有配置；将 `windows-es` 换成自动注册后显示的主机名或 SSH 登录名：

```powershell
ssh -n -p 2222 windows-es@proxy.example.com 'Get-Service | Where-Object Name -Match "elastic|kibana"'
ssh -n -p 2222 windows-es@proxy.example.com 'Get-ChildItem -LiteralPath "D:\Elasticsearch\logs"'
ssh -n -p 2222 windows-es@proxy.example.com 'Get-Content -LiteralPath "D:\Elasticsearch\logs\es-pro.log" -Encoding UTF8 -Tail 100'
ssh -n -p 2222 windows-es@proxy.example.com 'Select-String -LiteralPath "D:\Elasticsearch\logs\es-pro.log" -Pattern "ERROR|Exception" | Select-Object -Last 50'
```

路径按实际安装位置调整。明确指定文件编码；旧日志可能需要 `-Encoding Default`。读取大文件时使用 `-Tail` 或筛选条件。这里是把文件内容作为命令输出返回，并非 SFTP 文件传输。

多行脚本也作为一条命令发送，不通过 SSH stdin：

```powershell
$script = @'
Get-Service | Where-Object Name -Match 'elastic|kibana'
Get-Volume | Select-Object DriveLetter, SizeRemaining, Size
'@
ssh -n -p 2222 windows-es@proxy.example.com $script
```

## 任务生命周期和边界

- Windows Job Object 包含 PowerShell 及其子进程。用户脚本在进程加入 Job Object 后才执行；无法加入时拒绝执行。
- 超时、SSH 取消、连接断开或 Agent 退出会触发进程树清理。正常任务结束也不保留后台子进程。
- 网络中断不是瞬时可知的，Agent 可能要等心跳超时才清理任务。
- 取消或断网无法回滚已经执行的命令；断线时结果可能未知，必须检查现场后再决定是否重试。
- 修改服务器配置、禁用、删除或重新生成设备凭证会断开对应 Agent。旧 SSH 连接每次发起新任务时仍检查设备是否可用。
- 退出码 124 表示任务超时，125 表示调度/执行限制或不可用；成功收到正常任务退出消息时转发 PowerShell 退出码。连接异常可能使 SSH 客户端只收到连接关闭错误。
- 不支持借此启动常驻业务进程；首版用于短时排查。
- 这是通用命令执行能力，**并非强制只读工具**。排查应先运行只读命令；修改和重启按实际授权执行。
- 审计会记录命令和输出，不能自动识别所有密码。避免输出凭证文件、完整环境变量和含密码的配置；ES 认证应在目标机器本地处理。

## 验证

```powershell
go test -timeout 120s ./...
go vet ./...
```

Windows 测试包含真实 PowerShell 中文输出、错误码、长脚本、取消后子进程退出，以及完整的 SSH → WebSocket 通道 → PowerShell → 日志内容返回。测试通道使用本机 HTTP WebSocket；实际部署由配置加载器强制使用 WSS，公网 HTTPS 反向代理需要部署后另行验证。

### 2026-09-11 本机验证结果

- Windows 全量 `go test -timeout 120s ./...`、`go vet ./...`、前端构建与 lint 通过。
- 已验证真实 Windows Agent 读取中文日志，连续执行任务，8 MiB 输出上限终止任务，以及取消后子进程退出。
- 已验证独立设备凭证认证、凭证轮换失效、SSH 断线取消、忙时拒绝和审计输出。
- 本机 `go test -race` 因现有 MinGW 不支持 64 位编译而未能运行；Linux CI 已配置 race 检查，尚未执行远端 CI。
- 公网 WSS、目标服务器账号权限、目标 ES/Kibana 实际故障尚未验证。

## 192.168.102.7 升级记录（2026-09-11）

- 原版本：`v0.0.20`；升级为本次工作区构建（版本参数仍显示 `dev`，尚未发布 Git 标签）。
- 程序：`/usr/local/bin/claude-ssh-proxy`；数据库：`/data/claude-ssh-proxy/claude-ssh-proxy.db`。
- 保留现有 systemd unit，后台 `http://192.168.102.7:8080`、SSH `2222` 均保持原值。
- 隔离副本演练后停机备份，再替换程序。迁移自动补列，35 台旧主机全部保留为 `ssh` 接入。
- 已核对：35 台主机、3 份服务器凭证、3 份客户端凭证、36 条绑定、1 个管理员、1624 条历史审计全部保留。除服务器凭证由明文转为加密存储外，旧字段内容逐项核对一致；审计按原始字节比对，保留历史非 UTF-8 内容。
- 新建的 `claude-ssh-proxy.db.key` 是现有凭证的加密密钥，后续必须与数据库一起备份。管理员密码、JWT 设置和 SSH host key 均保留。
- 已验证新后台资源、未认证接口拒绝访问、SSH 握手、SQLite 完整性和外键，以及正式数据库副本的凭证解密。未逐台测试 35 台后端主机的实时连通性。
- 当前服务器没有 HTTPS 反向代理，尚未验证外部 Windows 的 WSS 接入；需要按上文配置可达域名、证书及 `/agent` 转发。
- 部署程序 SHA-256：`df270786e89a6fac7f82c5d357f085a230b780ed1b014ddb6ee09b821d661348`。
- 升级前完整备份：`/data/claude-ssh-proxy/20260911-agent-upgrade-zqhforcd/production-backup`。目录仅限 root 访问，包含旧数据库、旧程序、unit、host key 及当时存在的 SQLite 配套文件；旧版本当时没有 `.db.key`。
- 如需回退，应先停服务，再成套恢复该备份中的旧程序与数据库及配套文件。不能只换回旧程序继续使用已经加密的新数据库；升级后的新数据不会存在于升级前备份中。

## 本机 EXE 经实际服务器接入验证（2026-09-11）

- 链路：本机 `claude-ops-agent.exe` → `wss://192.168.102.7:18443/agent` → 已部署的跳板机 → SSH `2222` → 本机 PowerShell。
- 临时 TLS 转发到服务器 `127.0.0.1:8080`，使用测试证书并在本机当前用户证书库中建立信任；Agent 未跳过证书校验。此次验证的是局域网 WSS，不代表正式公网域名和证书已经部署。
- 实际返回主机名 `DESKTOP-E480IHH`，与本机一致；PowerShell 版本 `5.1.26100.9444`。
- 已通过：主机名查询、UTF-8 中文文件读取、错误流返回及退出码 7、PowerShell 版本查询。
- 服务端保留 4 条测试审计，均已完成，退出码分别为 `0、0、7、0`。测试代理名为 `windows-e2e-s3ahBbrs`。
- 本机测试 Agent、服务器临时监听均已停止，专用测试设备和客户端凭证已移除，测试配置已失效。原有主机未修改。
- 自动审批拦截了证书及临时文件删除，返回 `blocked by policy`，未提供更具体原因。当前用户 Root 证书库中的测试证书仍需手动移除，指纹 `55C80A83352F2133B05F832AA9E29213A52463AC`，主题 `CN=claude-agent-e2e-s3ahBbrs`。
- 本地测试目录：`dist/agent-e2e-20260911-yvo67hx0/`，已被 Git 忽略；服务器测试目录：`/data/claude-ssh-proxy/20260911-agent-e2e-s3ahBbrs/`，仅限 root。目录仍保留失效的测试配置、测试密钥和验证结果，不应提交到 Git。

手动移除本次测试证书（本机 PowerShell）：

```powershell
Remove-Item -LiteralPath 'Cert:\CurrentUser\Root\55C80A83352F2133B05F832AA9E29213A52463AC'
```

## Token 自动注册与全宽界面更新（2026-09-12 已部署）

- 网页新增“Agent 接入”：先选择 WSS 地址和允许访问的客户端凭证，再申请一次显示的接入 Token。
- 真实 EXE 仅传 `-token`，经 `192.168.102.7` 上的隔离候选服务自动注册为 `DESKTOP-E480IHH`。启动前服务器列表为空，启动后只有一个对应主机；SSH 文件读取、重启复用同一记录、撤销断开和审计全部通过。
- 隔离验证使用临时 `18444` WSS / `12222` SSH 端口，复用前次测试证书，没有新增系统证书信任。两个临时服务及本机测试 Agent 已停止。
- 主页面去掉固定最大宽度，表格随窗口铺满；列内容保持可读宽度，小屏在表格内部横向滚动。
- 使用样例数据在 Chrome 验证 1920、1440、1024、390 像素宽度：页面无横向溢出，主内容宽度等于视口宽度，表头未压缩换行。1920 像素下表格占用 1854 像素可用宽度。
- 常规 Go 测试、`go vet`、前端构建及 lint 均通过。候选程序在生产数据库副本上完成新增表迁移，所有既有业务表字段内容核对一致。
- Linux 候选 SHA-256：`db74156c9f5a23173f4b63d765641050374eb6038ae8802b47cb986c60a03276`。
- 2026-09-12 经用户确认允许中断现有连接后，停止正式服务、完成一致性备份并替换上述候选程序，随后启动成功。保留原 systemd unit、Web `8080`、SSH `2222`。
- 升级前完整备份：`/data/claude-ssh-proxy/20260912-enrollment-production-backup-a9r98cmw`，目录仅限 root，包含旧程序、unit、数据库、加密密钥、host key 及停机时存在的 SQLite 配套文件；`manifest.json` 记录恢复路径，`verification.json` 记录部署检查结果。回退需先停服务，成套恢复程序、数据库及配套文件，再启动；升级后的新增数据不在该备份内。
- 正式库升级后逐项核对完全一致：39 台主机、3 份服务器凭证、3 份客户端凭证、39 条绑定、1 个管理员、1 条设置和 1651 条审计。SQLite 完整性和外键检查通过，加密密钥和 SSH host key 未变。
- 服务状态为 `active/running`，自动重启次数为 0。服务端及本机均验证首页 HTTP 200、新版 Token 页面资源和自适应表格样式；未认证的 `/api/me`、`/api/agent-enrollments`、`/agent` 均返回 401，SSH `2222` 握手正常。未逐台测试后端主机连通性。
- 后台地址：`http://192.168.102.7:8080`。正式 HTTPS/WSS 入口仍待配置；此前隔离环境的自动注册验证不代表公网入口已就绪。

## 项目更名与迁移（2026-09-14）

- 当前项目、后台标题、Go 模块、Linux 程序与新安装 systemd 服务统一为 `ops-ssh-proxy`；Windows 二进制为 `ops-ssh-agent.exe`。
- 前端改用 Ant Design，包含侧栏导航、分页表格、固定操作列、筛选搜索、统一弹窗和表单；“监听设置”更名为“服务设置”，自注册 Token 收入该页。
- 新安装默认数据库为 `ops-ssh-proxy.db`。直接运行时若检测到当前目录有旧 `claude-ssh-proxy.db`，程序要求显式指定 `-db`，避免误建空库。
- 旧部署升级应先读取实际 unit 参数，停服务并备份程序、unit、数据库及 SQLite 配套文件、`.db.key` 和 host key。新程序可继续使用原 `-db`、`-host-key` 和监听参数，不要求改名历史数据文件。
- 如同时更名 systemd 服务，先停用旧服务，再创建 `ops-ssh-proxy.service`，用新程序路径和原有数据路径/监听参数启动。不要让两个服务同时访问同一数据库。成功后核对主机、凭证、审计及密钥；失败时停新服务，成套恢复备份并启用旧服务。
- 历史部署记录中的旧路径和备份名称保留为实际路径，不随产品改名替换。Git 远端仓库地址仍指向实际仓库，本次没有更名远端仓库。2026-09-14 经用户授权，以 `v0.0.24` 发布此轮更新。
- 发布前验证时，生产环境为 2026-09-12 部署版本；正式切换结果见后续部署记录。

### 本轮验证（2026-09-14）

- Go 全量测试和 `go vet` 通过。共享 Token 已验证多主机注册、并发注册去重、大小写重连、名称冲突拒绝、无权限拒绝、轮换/停用失效、删除后不复活。
- 本机真实 Windows PowerShell 经共享 Token、自定义代理登录名和 SSH 读取中文文件成功。`ops-ssh-agent.exe -help` 已确认提供 `-token` 和 `-hostname`。本轮集成测试使用隔离的本机 WebSocket，没有新建证书信任或验证正式公网 WSS。
- Ant Design 前端构建、lint 通过。Chrome 验证 1920、1440、1024、390 像素宽度，页面均无横向溢出，表格每页 20 条；搜索、编辑弹窗、Token 生成/复制、取消轮换、停用、移动端导航均通过，无浏览器运行错误。截图和交互结果在本地 `dist/ui-check-20260914/`，仅使用样例数据。
- `192.168.102.7` 上生产库副本隔离演练通过：38 台主机、3 份服务器凭证、3 份客户端凭证、39 条绑定、1 个管理员、1651 条审计及旧接入表内容全部保留；完整性与外键检查通过，新增 3 张自注册表。副本为隔离监听新增/修改了 `listen_addr`，正式数据库和服务未变。
- 演练目录：`/data/claude-ssh-proxy/20260914-ops-review-3yhyk482`，仅限 root；包含副本数据库及其密钥、候选程序和 `verification.json`。隔离进程已停止。
- 候选 Linux 程序 SHA-256：`7be9642d706a84ecaaedc4528eced51af491279d5263137b8c653a8c62c2a465`。安装脚本已通过 Linux Bash 语法检查，并强制保留 LF 换行。

## v0.0.24 正式发布与部署（2026-09-14）

- 用户明确授权发版部署后，提交 `f9d87f7897c6cfabdedf247eaadfcd0b80ee91e7` 并推送 `v0.0.24` 标签。[GitHub Release](https://github.com/kingsh2012/claude-ssh-proxy/releases/tag/v0.0.24) 已发布 Linux 服务端和 Windows Agent 安装包。
- [CI](https://github.com/kingsh2012/claude-ssh-proxy/actions/runs/34769505093) 成功：Linux 全量 race 测试、Windows Agent/SSH 集成测试、构建和 vet 通过；[Release 构建](https://github.com/kingsh2012/claude-ssh-proxy/actions/runs/34769508876) 成功。
- 生产环境使用 GitHub Release 的正式 Linux 产物，部署前已与发布资产 SHA-256 核对。程序为 `/usr/local/bin/ops-ssh-proxy`，`-version` 返回 `v0.0.24`。
- 新服务 `ops-ssh-proxy.service` 已启用开机启动，状态 `active/running`，自动重启次数 0；旧 `claude-ssh-proxy.service` 已停止并禁用，旧 unit 和程序保留用于回退。
- 数据库仍为 `/data/claude-ssh-proxy/claude-ssh-proxy.db`；数据库密钥、SSH host key、后台 `http://192.168.102.7:8080` 和 SSH `2222` 保持原值。未重新生成账号或凭证。
- 切换前没有活动 SSH 连接。停机后完成一致性备份，位置：`/data/claude-ssh-proxy/20260914-v0.0.24-deploy-01vsp_yc/production-backup`。备份含旧程序、unit、数据库及停机时存在的 SQLite 配套文件、`.db.key` 和 host key，仅限 root。
- 正式库迁移后逐项核对一致：38 台主机、3 份服务器凭证、3 份客户端凭证、39 条绑定、1 个管理员、1 条设置、1651 条审计，以及两张旧接入表。新增 3 张自注册表，默认没有启用共享 Token。完整性、外键和密钥摘要检查通过。
- 本机与服务端均确认新版页面可访问，正式设置资源包含 `ops-ssh-agent.exe`、`-hostname`；未认证 `/api/me`、`/api/settings/agent-registration`、`/agent` 均返回 401，SSH 握手正常。没有逐台测试后端服务器连通性。
- 服务器部署目录包含 `release.json`、`deployment-verification.json` 和部署回退脚本。若需回退，先停新服务，再成套恢复备份中的旧数据库、配套文件及密钥，启用旧服务；不要用旧数据库覆盖仍运行的服务。
- 正式程序 SHA-256：`709136f4eb0c65a35d720b71299cb634b743c75cb96cf6af08386ae2efc06eef`。
- Linux 发布包 SHA-256：`838fd3997eb8b04b24e2ffabb996b8944baa75308e647ba505ff94f3eb127b85`；Windows 发布包 SHA-256：`a8f0418066b756f7931968c14affc4bbf5696d497e1fc0b37074323989a2b915`。本地 `dist` 的同名二进制和安装包已同步为正式发布产物。
- 正式 HTTPS/WSS 入口仍待配置。在“服务设置”填写实际可达的 WSS 地址与默认客户端凭证后生成共享 Token；本次未将测试地址写入生产配置。

## Ant Design Pro 精简后台（2026-09-14，v0.0.25 已部署）

- 根据新的界面要求，前端改为 Ant Design Pro 官方 Umi Max 骨架。使用 ProLayout 默认浅色混合布局、PageContainer、ProTable、LoginForm；只保留六个实际业务页面，移除示例、mock 与样式设置工具。来源、许可证和开发入口见 [前端说明](../webui/README.md)。
- 页面全宽自适应，窄屏仅表格内部横滚。连接失败只在鼠标悬停时展示完整报错，无点击展开和错误输入框。
- 继续使用现有 API；登录、首次强制改密、退出、会话失效及旧 Agent 页面跳转均保留。自注册 Token 仍在“服务设置”管理。
- 本机 `npm ci`、TypeScript 检查、lint、生产构建通过。Chrome 使用样例 API 验证 1920、1440、1024、390 像素宽度、每页 20 条、搜索与接入方式筛选、添加弹窗、Token 生成、登录和首次改密、六个页面、审计命令展开；无浏览器运行错误。截图和结果保存在本地 `dist/ui-pro-20260914/`，不是生产数据。
- 已发布 [v0.0.25](https://github.com/kingsh2012/claude-ssh-proxy/releases/tag/v0.0.25)，代码提交 `192b6aef6ab1301f6b7a9dc9886a8eaa569df928`。[CI](https://github.com/kingsh2012/claude-ssh-proxy/actions/runs/34772979254) 的 Linux 和 Windows 检查全部通过，[Release 构建](https://github.com/kingsh2012/claude-ssh-proxy/actions/runs/34772979193) 成功。
- `192.168.102.7` 已部署正式 Release Linux 产物，`ops-ssh-proxy.service` 为 `active/running`、开机启用，自动重启次数 0，程序版本 `v0.0.25`。后台仍为 `http://192.168.102.7:8080`，SSH 端口仍为 `2222`。
- 发布包 SHA-256 与 GitHub 资产摘要一致。Linux tar.gz：`7e92137517eeb34085920cf4c4598ca69dbeb6c70ed98e3f781fc3d06202bc5d`；Windows ZIP：`e65dfbc3b4827a28d8c2594f67d1d43dfb10e6e15b7f1880af052d37ab02e0b3`；正式服务端程序：`edb8004a4dd26507d85c996a07ff408c1c6f8cd3dbb85a68bc2c81c34423352f`。
- 升级前已停止服务并一致性备份至 `/data/claude-ssh-proxy/20260914-v0.0.25-deploy-_txsvoco/production-backup`。38 台主机、3 份服务器凭证、3 份客户端凭证、39 条绑定、1 个管理员、1651 条审计及其他表逐项保留；数据库完整性和外键检查通过。数据库密钥、SSH host key 和 systemd unit 均未改变。
- 生产返回的新 Umi 入口、JS/CSS、SSH 握手和未登录接口鉴权检查通过。Chrome 使用正式生产静态资源并拦截业务 API 为样例数据，复验四种宽度、筛选、Token 表单、登录与首次改密、审计展开和悬停报错；未对生产业务数据执行这些测试操作。另以未拦截 API 的真实浏览器访问生产登录页通过，均无页面运行错误。
- 本地发布包、截图和验证结果在 `dist/20260914-v0.0.25/`；服务器同次部署目录保存发布信息、部署脚本和 `deployment-verification.json`。公网 WSS 入口尚未配置，本次 UI 发版没有生成生产自注册 Token。


## 绿色主题与原生 ProTable 交互（2026-09-14，v0.0.26 已部署）

- 按用户指定，登录 VPS `45.32.199.207` 阅读 `/root/yalule-admin/docs/frontend-design/`，并参考实际主题、全局样式、工具栏和用户管理页面；源码参考提交 `0bb6c64c6f72b8cb2a19fc09fbeede018ad32538`。样式参数和本项目适配边界见 [前端说明](../webui/README.md)。
- 用户最终确定绿色品牌主题和纯文字品牌，不使用图片 Logo。顶栏 48px、侧栏 208px、页面边距 12px；工作区使用与面包屑相接的单层白底表面，服务设置取消同级嵌套卡片。
- 五个表格页使用 ProTable 原生工具栏和列设置；主机的接入方式、启用状态和连接结果移到列头筛选。搜索回车生效、清空立即撤销，查询与分页写入 URL。默认每页 50 条，列设置由 ProTable 自身持久化。
- 服务器和两类凭证的新建、编辑改用 ModalForm；危险行内动作统一收进三点菜单，保留具体对象确认。Agent 自动注册、Token 管理、CSV 导入和连接报错仅悬停查看保持现有语义。
- 验证通过：lint、TypeScript、生产构建和 Go 嵌入构建；Chrome 在 1920、1440、1024、390 像素宽度均无整页横向溢出。验证了原生列设置持久化、URL 筛选恢复、50 条分页与搜索归位、主机和凭证表单提交参数、表单状态隔离、公钥名称提取、Token 生成、登录与首次改密、审计展开和报错悬停。业务 API 使用样例数据，未在生产执行测试写操作；无页面运行错误。截图和结果在本地 `dist/20260914-green-ui/`。
- 已发布 [v0.0.26](https://github.com/kingsh2012/claude-ssh-proxy/releases/tag/v0.0.26)，代码提交 `86380ad0ddeab5f73a71146ba6ccad55a3738df0`。[CI](https://github.com/kingsh2012/claude-ssh-proxy/actions/runs/34775151652) 和 [Release 构建](https://github.com/kingsh2012/claude-ssh-proxy/actions/runs/34775151910) 全部通过。
- `192.168.102.7` 已安装正式发布包，程序版本 `v0.0.26`；`ops-ssh-proxy.service` 为 `active/running`，开机启用，自动重启次数 0。后台 `http://192.168.102.7:8080` 和 SSH `2222` 保持原值。
- 发布包摘要与 GitHub 资产一致。Linux tar.gz SHA-256：`79a8a062ceeb5151742ae0977fd4d3bd4cfd099759c3ac6f88cb305b4e9c30f2`；Windows ZIP：`ca75e181a5183b2deb1f3181d1014822ed78f1d0c746f0c08ce1961db902a17f`；服务器二进制：`56b9cbbe1bad2054449a4c676695d02652c92758335263754f7dcbc7bb743edb`。
- 升级前停服一致性备份位于 `/data/claude-ssh-proxy/20260914-v0.0.26-deploy-ejob4_ss/production-backup`。38 台主机、3 份服务器凭证、3 份客户端凭证、39 条绑定、1 个管理员、1651 条审计及其他表逐项保留；完整性和外键检查通过。密钥、SSH host key、数据库路径和服务配置均未改变。
- 生产服务的 HTTP 资源、鉴权和 SSH 握手检查通过；真实未登录浏览器访问登录页正常，主按钮实际颜色为 `rgb(21, 128, 61)`。使用生产静态资源配合样例 API 复验四种宽度、列设置保存、URL 筛选、分页、表单提交与隔离、Token 和登录流程，无页面运行错误，测试未修改生产业务数据。
- 发布资产、截图和检查结果保存在本地 `dist/20260914-v0.0.26/`；服务器同次部署目录保留发布信息、部署脚本和验证结果。生产公网 WSS 尚未配置，本次没有生成生产自注册 Token。


## v0.0.28 发布与部署（2026-09-14）

- 已部署到 `192.168.102.7:8080`，代码提交 `34b9da0`。包含自注册草稿密钥与独立地址保存、注册后手动授权、启动命令自动填入 Token、列表静默刷新、启禁用状态强调、服务器跨页多选批量禁用/删除，以及统一“凭证”“新建”文案。高级设置位于主机表单末尾。
- [正式发布](https://github.com/kingsh2012/claude-ssh-proxy/releases/tag/v0.0.28)；[CI](https://github.com/kingsh2012/claude-ssh-proxy/actions/runs/34801748567) 与 [Release](https://github.com/kingsh2012/claude-ssh-proxy/actions/runs/34801748543) 均成功。v0.0.27 是过程中生成的发布包，未部署。
- 从 v0.0.26 直接升级，备份位于 `/data/claude-ssh-proxy/20260914-v0.0.28-deploy-7eehtked/production-backup`。38 台主机、两类各 3 份凭证、39 条授权关联、1 个管理员和 1651 条审计逐项保留；数据库完整性、外键、密钥和 systemd 配置检查通过。
- 服务为 active/running，NRestarts=0；HTTP 静态资源、未登录接口 401 和 SSH 握手通过。正式静态资源配合模拟业务 API 验证了多选禁用、部分删除失败保留选择及重试、表单顺序、Token 生成/保存/刷新恢复、静默刷新与展开保留、宽窄屏；浏览器验证未修改生产业务数据。
- 本机 Windows 集成测试验证：自注册后 SSH 拒绝访问，手动关联客户端凭证后可读取 Windows 文件。生产 WSS 域名仍未配置，本次未生成生产 Token 或修改生产授权。
- 服务端二进制 SHA256：`bfb2f983dd990d660b258a0651b5183a06a418edf41d37bcf25f35a689390935`。本机 `dist/` 已同步正式服务端与 Windows Agent 包；验证记录保存在 `dist/20260914-v0.0.28/`。


## v0.0.29 发布与部署（2026-09-14）

- 已部署到 `192.168.102.7:8080`，代码提交 `729b22d`；增加服务器批量解禁，跳过已启用项。设置页左侧监听和密码表单独立排列，修复右侧生成 Token 导致密码区下移 20px 的问题。
- [正式发布](https://github.com/kingsh2012/claude-ssh-proxy/releases/tag/v0.0.29)；[CI](https://github.com/kingsh2012/claude-ssh-proxy/actions/runs/34807808259) 与 [Release](https://github.com/kingsh2012/claude-ssh-proxy/actions/runs/34807808027) 均成功。
- 从 v0.0.28 升级，备份：`/data/claude-ssh-proxy/20260914-v0.0.29-deploy-64ewc_3b/production-backup`。全部原有数据逐项保留，包括 38 台主机、39 条授权关联、1651 条审计及现有自注册设置；数据库完整性、外键、主机密钥、数据库密钥和 systemd 配置检查通过。
- 服务 active/running，NRestarts=0。正式静态资源配合模拟业务 API 验证批量解禁与跳过已启用项、批量禁用/删除失败重试、生成 Token 前后密码区纵坐标不变、宽窄屏与既有交互；未通过页面测试修改生产数据。
- 服务端 SHA256：`35610927b718c7254e66fc7b64a6f51ec997117413f46a02ddb59e05725fd100`。本机 `dist/` 已同步正式程序和发布包，验证材料位于 `dist/20260914-v0.0.29/`。


## v0.0.31 发布与部署（2026-09-14）

- 已部署到 `192.168.102.7:8080`，代码提交 `7f33d29`。多选列缩至 32px；服务器启用/禁用、复制和删除直接显示；批量操作移入工具栏下拉框，取消选中后的额外提示行。两类凭证“更多操作”菜单中的删除项统一为“删除”。
- [正式发布](https://github.com/kingsh2012/claude-ssh-proxy/releases/tag/v0.0.31)；[CI](https://github.com/kingsh2012/claude-ssh-proxy/actions/runs/34808731211) 与 [Release](https://github.com/kingsh2012/claude-ssh-proxy/actions/runs/34808731105) 均成功。v0.0.30 为追加文案前的中间发布，未部署。
- 从 v0.0.29 升级，备份：`/data/claude-ssh-proxy/20260914-v0.0.31-deploy-_c9e6b7c/production-backup`。40 台主机、41 条授权关联、1655 条审计、现有凭证及自注册设置逐项保留；数据库完整性、外键、密钥和服务配置检查通过。
- 服务 active/running，NRestarts=0。正式静态资源配合模拟业务 API 验证多选列宽、行内按钮、选中时表格不下移、批量菜单禁用/启用/删除及失败重试、两类凭证删除菜单文案，以及设置页和静默刷新回归；页面验证未修改生产业务数据。
- 服务端 SHA256：`123c9333d592da6c70a4021e0095a7a741aa54a5255a06a1527a20ad9524125f`。本机 `dist/` 已更新正式程序及发布包，验证材料位于 `dist/20260914-v0.0.31/`。
