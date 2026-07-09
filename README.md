# claude-ssh-proxy

一个给 AI Agent(如 Claude)使用的 SSH 反向代理:Agent 用一个代理登录名连接到 proxy,proxy 校验身份后自动路由、连接到真正的目标机器,并把 Agent 在会话里执行的操作记录成审计日志。同时内置一个 React + Tailwind 的 Web 管理后台,用来维护路由、监听地址和查看审计记录。

## 解决什么问题

直接把 SSH 私钥/密码交给 AI Agent、让它一台台机器手动连,会有几个问题:

- 每台机器的地址、密钥、跳板机配置分散,Agent 每次都要自己拼
- 密码认证还要靠 `sshpass`,密码容易明文出现在进程列表、日志、对话上下文里
- 没有集中的操作审计,不知道 Agent 具体执行了什么命令

`claude-ssh-proxy` 把这些收敛到一层:

- Agent 只需要知道一个"代理登录名"(比如 `abc`),不需要知道真实的目标 IP、账号、密码/私钥
- 代理登录名到目标机器的映射、目标机器的认证信息,统一在 Web 后台配置和保管
- 每一次 `exec`/`shell`/`subsystem` 操作都会记录:来源 IP、代理登录名、目标机器、具体命令或会话内容、退出码

## 架构

```
        SSH(公钥或密码)              SSH(密码或私钥,由 proxy 保管)
Claude ───────────────────▶ proxy ───────────────────────────▶ 目标机器 1
                              │
                              └──────────────────────────────▶ 目标机器 2
                                                                  ...
```

- Agent 登录 proxy 时使用的用户名是一个"代理登录名",与目标机器上的真实用户名无关
- 客户端凭据(公钥或密码)是独立管理的"身份"(比如某个 Claude Agent),和服务器是多对多关系:一份凭据可以关联多台服务器,一台服务器也可以被多份凭据共用,任一凭据匹配即可登录;两个方向都能编辑关联关系(服务器页面勾选凭据,或者凭据页面勾选服务器)
- proxy 连目标机器要用的信息(SSH登录名 + 密码/私钥)都收在"服务器凭据"里,一份凭据可以被多台服务器共用,改一处、全部生效;服务器本身不再单独存密码/私钥
- 每台服务器可以单独启用/禁用;禁用后不管客户端凭据对不对,一律拒绝这个代理登录名登录,不用删掉配置就能临时"拔网线"
- 所有配置(路由、服务器凭据、客户端凭据、管理员账号、审计日志)存在 SQLite 单文件数据库里,改配置即时生效,不需要重启 SSH 监听(除非改的是监听地址本身)

## 快速开始

### 1. 编译

需要 Go 1.23+ 和 Node 20+。

```bash
cd webui
npm install
npm run build   # 产出 webui/dist,会被 go:embed 打进最终二进制
cd ..
go build -o claude-ssh-proxy .
```

### 2. 启动

```bash
./claude-ssh-proxy
```

默认:
- SSH 监听 `:2222`
- Web 管理后台监听 `:8080`
- 数据库文件 `claude-ssh-proxy.db`(当前目录)

首次启动会自动创建一个管理员账号,固定是 `admin` / `admin`:

```
========================================
已创建初始管理员账号,首次登录后会强制要求修改密码:
  用户名: admin
  密码:   admin
========================================
```

打开 `http://<部署机器>:8080` 用 `admin`/`admin` 登录。数据库里有一个"是否已初始化"标记,首次登录时这个标记是 0,前端会强制跳转到"修改密码"页面,不展示其他任何页面;改完密码后标记才会变成 1,之后才能正常使用路由管理、监听设置、审计日志等页面。

### 3. 先建一份服务器凭据

在"服务器凭据"页面点"添加服务器凭据",填写:

- **名称**:随便起,比如"生产环境统一密码"
- **SSH登录名**:登录目标机器用的用户名,比如 `root`
- **认证方式**:密码或私钥
- **哪些服务器使用这份凭据**:勾选框,可以先留空,后面加服务器的时候再关联

一份凭据可以被多台服务器共用,改一处、全部生效。凭据正在被服务器使用时无法删除,需要先把引用它的服务器改成其他凭据;取消勾选某台服务器也会有二次确认提示(取消后这台服务器的认证信息会变空,需要单独重新关联一份凭据)。

### 4. 添加一台目标机器

在"服务器"页面点"添加服务器",填写:

- **代理登录名**:Agent 连 proxy 时用的用户名,比如 `abc`,唯一
- **目标机器 IP/端口**:真实要连的机器,比如 `192.168.1.2:22`
- **服务器凭据**:下拉选上一步建好的凭据(提供SSH登录名和密码/私钥),也可以先不选,以后再补
- **哪些客户端凭据能登录这台服务器**:勾选框,见下一步

关联关系两个方向都能编辑:服务器编辑表单里选凭据,或者反过来在"服务器凭据"页面勾选哪些机器用它。

已有的服务器可以点"复制",把这一行的目标机器/端口/凭据关联都带到新建表单里,只需要改一下代理登录名(唯一)就能保存,适合批量加同类机器。不想用了也不用删,点"禁用"就行,禁用的服务器无论凭据对不对都会被直接拒绝登录。

批量添加服务器可以用"导入"功能:点"导入"弹出一个 CSV 粘贴框,格式是:

```
route_user,target_host,target_port,server_credential_id,client_credential_id
srv1,192.168.1.2,,1,1;2
srv2,192.168.1.3,22,,3
srv3,192.168.1.4,,,
```

`route_user` 是唯一键,已存在就覆盖更新,不存在就新增;`target_port`/`server_credential_id`/`client_credential_id` 留空分别默认 22、不关联服务器凭据、不关联客户端凭据;`client_credential_id` 一个格子里可以用分号分隔关联多个客户端凭据。服务器凭据/客户端凭据的 ID 在各自页面的"ID"列能看到。提交前会先校验格式(表头、必填、端口范围、引用的 id 是否存在),校验不通过不会调用任何接口;校验通过后逐行导入,单独一行失败不影响其他行,结果里会列出每行是新增/更新/失败。

### 5. 添加客户端凭据,关联到这台机器

在"客户端凭据"页面点"添加客户端凭据",填写:

- **认证方式**:公钥或密码
  - 公钥:粘贴 Agent 侧私钥对应的公钥,名称会自动从公钥末尾的 comment 截取(可以手动改)
  - 密码:给这份凭据设一个共享密码
- **能登录哪些服务器**:勾选框,想让这份凭据能连几台机器就勾几个;一份凭据可以关联多台服务器,一台服务器也可以被多份凭据共用

这个关联关系在"服务器"页面编辑某台机器时也能反过来勾选,两边改的是同一份数据。

### 6. 让 Agent 连接

把 Agent 侧的私钥交给 Claude(或者告诉它用密码),让它这样连:

```bash
ssh -p 2222 abc@<proxy-ip>
```

Agent 之后执行的每条命令、每个交互式 shell 会话,都会被记录进审计日志,可以在 Web 后台的"审计日志"页面查看。

## 常用参数

```
./claude-ssh-proxy \
  -db claude-ssh-proxy.db \        # SQLite 数据库路径
  -host-key host_key \             # proxy 自身 SSH host key 文件(不存在会自动生成)
  -web-addr :8080 \                # Web 管理后台监听地址
  -bootstrap-admin-user admin \    # 首次启动自动创建的管理员用户名
  -bootstrap-admin-password admin  # 首次启动自动创建的管理员初始密码(登录后强制要求修改)
```

SSH 监听地址不是启动参数,而是存在数据库里的一项设置,首次启动默认 `:2222`,之后可以在 Web 后台"监听设置"页面修改,修改后立即热切换,不需要重启进程。

## 用 systemd 常驻运行

仓库里 `systemd/claude-ssh-proxy.service` 是一份现成的 unit 文件,默认把数据库和 host key 放在 `/var/lib/claude-ssh-proxy`,Web 后台只监听 `127.0.0.1:8080`(建议在前面套一层反向代理做 TLS 再对外暴露)。安装步骤:

```bash
sudo useradd --system --home /var/lib/claude-ssh-proxy --shell /usr/sbin/nologin claude-ssh-proxy
sudo mkdir -p /var/lib/claude-ssh-proxy
sudo chown claude-ssh-proxy:claude-ssh-proxy /var/lib/claude-ssh-proxy

sudo cp claude-ssh-proxy /usr/local/bin/claude-ssh-proxy
sudo cp systemd/claude-ssh-proxy.service /etc/systemd/system/

sudo systemctl daemon-reload
sudo systemctl enable --now claude-ssh-proxy
sudo journalctl -u claude-ssh-proxy -f   # 看启动日志里打印的初始管理员密码
```

SSH 监听端口(默认 `:2222`)本身不需要特权,如果你把它改成 1024 以下的端口,unit 文件里已经带了 `CAP_NET_BIND_SERVICE`,不需要用 root 跑。

## 目录结构

```
.
├── main.go            # 入口:初始化数据库、启动 SSH proxy 和 Web 服务
├── store.go            # SQLite 存储层:路由、管理员账号、审计日志
├── auth.go             # proxy 侧认证:公钥/密码校验
├── proxy.go            # SSH 反向代理核心:接受连接、按用户名路由、双向转发
├── audit.go            # 审计日志采集(exec 命令、shell 会话)
├── keys.go             # host key 生成、私钥解析
├── api.go              # Web 管理后台的 HTTP API
├── staticfs.go         # 用 go:embed 把前端产物打进二进制
└── webui/              # React + Tailwind 前端源码
```

## 安全注意事项

- 目标机器的密码/私钥目前以明文存在 SQLite 文件里,请确保这台部署机器本身足够可信,并做好文件权限和备份加密
- Web 管理后台目前是明文 HTTP,建议只在内网访问,或者在前面套一层反向代理做 TLS
- proxy 连目标机器时默认不校验目标机器的 host key(`InsecureIgnoreHostKey`),内网场景下影响有限,对安全性要求更高可以自行改造成校验固定指纹

## CI/CD

- `.github/workflows/ci.yml`:每次 push / PR 到 `main` 分支,自动构建前端 + `go vet` + `go build` + `go test`
- `.github/workflows/release.yml`:推送 `vX.Y.Z` 格式的 tag(例如 `v0.0.1`)会自动触发,编译 Linux amd64 版本的二进制,打包成 `.tar.gz` 并发布到 GitHub Release

发布新版本:

```bash
git tag v0.0.2
git push origin v0.0.2
```
