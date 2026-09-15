# aiagent-ssh-proxy管理后台

基于 [Ant Design Pro](https://github.com/ant-design/ant-design-pro) 6.0.3 官方精简骨架重建，参考提交 `adfd44085738ca953573a13322c1ba84aca8b9e3`（2026-09-14）。保留Umi Max路由与构建、ProLayout混合布局、PageContainer、ProTable和LoginForm；移除官方示例、mock、图表、国际化示例、OpenAPI和样式设置抽屉。官方许可证见 `ANT-DESIGN-PRO-LICENSE`。

## 开发

需要Node.js 22 或更新版本。

```sh
npm ci
npm run dev
npm run typecheck
npm run lint
npm run build
npm run preview
```

- `config/config.ts`：Umi配置，开发 `/api` 代理到 `127.0.0.1:8080`。
- `config/routes.ts`：业务路由；`config/defaultSettings.ts`：参考项目布局；`config/reportTheme.ts`：绿色主题。
- `src/layouts/Console.tsx`：登录检查、首次改密、会话失效和菜单。
- `src/pages/`：路由入口，现有业务页面与API封装保留在 `src/`。
- `src/app.tsx`：中文组件与消息上下文。
- `src/index.css`：业务单元布局及窄屏适配，不覆盖Pro导航和表格主题。
- 生产输出保持 `webui/dist`，继续由Go `staticfs.go` 嵌入，不改变部署入口。
- `npm ci` 自动运行 `max setup`；`.umi` 与 `.umi-production` 为生成内容，不提交。

后台保留服务器、两类凭证、当前连接、审计、服务设置六个页面。统一服务器自注册在服务设置中维护。连接错误仅悬停显示完整内容。

## 绿色主题与交互规范（2026-09-14）

本轮样式参考VPS `45.32.199.207:/root/yalule-admin/docs/frontend-design/` 的当前规范和 `frontend/config/reportTheme.ts`、`defaultSettings.ts`、`src/global.less`、用户管理页面。参考源码提交：`0bb6c64c6f72b8cb2a19fc09fbeede018ad32538`。

- 用户明确覆盖参考主题：品牌改为绿色，主色 `#15803D`、选中背景 `#DCFCE7`，顶栏只显示 `aiagent-ssh-proxy` 文字，不显示Logo或头像；favicon使用空数据地址，移除模板紫色图标。
- 服务器、两类凭证、连接与审计使用标准表格型；服务设置使用特殊控制型。顶栏 48px、侧栏 208px、页面边距 12px、控件 28px、正文 14px、工作表面圆角 6px。
- 表格使用ProTable原生 `headerTitle + toolBarRender + options.setting`，不再有独立工具栏。关闭密度/全屏入口与独立搜索表单，与参考规范一致。列显隐与顺序由ProTable按页面持久化，主标识与操作列不可隐藏。
- 主机关键词按回车提交，清空立即撤销筛选。接入方式、启用状态、测试结果在列头筛选；审计的代理登录名、目标地址和客户端凭证在列头输入筛选。查询、排序与分页同步URL。
- 本项目主机和凭证接口现有契约是全量返回，继续在完整数据集上过滤与分页；审计继续调用原有服务端筛选接口，最多 200 条。不套用参考项目的分页API或改动后端契约。
- 分页默认 50 条，可选 50 / 100 / 300 / 500；筛选和每页数量变化回到第一页。每列使用数值宽度，整表滚动下限按列宽求和。
- 普通新建/编辑使用ModalForm，敏感字段编辑留空，关闭销毁；上传CSV仍使用普通Modal。服务器和两类凭证的删除均在三点菜单内；均保留对象明确的确认框。
- 实现分工：`ListControls.tsx` 提供搜索和图标操作；`useListView.ts` 管理URL查询和标准表格参数；`ServerForm.tsx`、`CredentialForms.tsx` 管理表单。

## 列表批量操作（2026-09-14）

- 服务器列表支持跨页多选、批量禁用、批量启用、批量删除，以及批量修改服务器凭证和客户端凭证；确认框列出选中主机，成功项取消选择，失败项保留选择并显示原因。
- 批量修改凭证使用弹窗选择凭证和操作方式。服务器凭证是单值，只支持覆盖或删除；删除会清空所选SSH服务器当前关联的凭证。客户端凭证是多值集合，覆盖会整体替换，新增会保留现有关联并追加所选凭证，删除只解除所选凭证。服务器凭证操作跳过Agent服务器，客户端凭证操作适用于SSH和Agent服务器。
- 批量禁用跳过已禁用主机，批量启用跳过已启用主机。新建按钮统一显示“新建”，认证相关界面统一使用“凭证”。

- 多选列宽 32px；选中后不额外显示提示行，工具栏“批量操作”下拉框显示选中数量并提供禁用、启用和删除。行内编辑、测试、启用/禁用、复制直接展示，删除放在三点菜单内；Agent不提供复制。

## 搜索与审计详情（2026-09-14）

- 两类凭证复用服务器列表的工具栏搜索：回车提交、清空撤销、关键词同步URL、搜索后回到第一页。服务器凭证搜索名称、SSH登录名和绑定主机；客户端凭证搜索名称和绑定主机。
- 审计通过“详情”打开最大 1280px的弹窗，替代行内展开。输入和输出使用深色终端配色、独立滚动，仍显示截断提示；每 5 秒同步可见记录的更新，记录离开最近 200 条后保留已打开的内容。
- 公网地址输入框支持HTTPS主域名或WSS地址，保存后显示实际WSS接入地址；高级配置使用右侧浅灰小箭头。

## 服务器自注册与操作菜单（2026-09-15）

- 服务器自注册支持`https://proxy.example.com`和`wss://proxy.example.com/agent`；保存后显示实际WSS地址，公网只需转发`/agent`。
- 页面中文与英文之间不留空格，例如“SSH监听地址”。
- 服务器列表的删除移入三点菜单，沿用删除确认和批量操作期间禁用规则。

## 独立监听与Agent内置TLS（2026-09-15）

- 服务设置中的“Web与Agent监听”分别配置Web和Agent地址，保存后重启服务生效；Web仅HTTP，Agent可启用内置TLS并设置服务器证书、私钥文件路径。
- 公网IP映射使用`wss://公网IP:端口/agent`，仅映射Agent端口；证书包含该公网IP，客户端构建时嵌入签发CA公钥证书。具体生成、构建和迁移方法见[Windows接入说明](../docs/20260911-windows-agent.md#2-独立web与agent监听内置tls2026-09-15)。
