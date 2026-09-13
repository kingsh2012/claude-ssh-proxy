# ops-ssh-proxy 管理后台

基于 [Ant Design Pro](https://github.com/ant-design/ant-design-pro) 6.0.3 官方精简骨架重建，参考提交 `adfd44085738ca953573a13322c1ba84aca8b9e3`（2026-09-14）。保留 Umi Max 路由与构建、ProLayout 默认浅色混合布局、PageContainer、ProTable 和 LoginForm；移除官方示例、mock、图表、国际化示例、OpenAPI 和样式设置抽屉。官方许可证见 `ANT-DESIGN-PRO-LICENSE`。

## 开发

需要 Node.js 22 或更新版本。

```sh
npm ci
npm run dev
npm run typecheck
npm run lint
npm run build
npm run preview
```

- `config/config.ts`：Umi 配置，开发 `/api` 代理到 `127.0.0.1:8080`。
- `config/routes.ts`：业务路由；`config/defaultSettings.ts`：Pro 默认布局。
- `src/layouts/Console.tsx`：登录检查、首次改密、会话失效和菜单。
- `src/pages/`：路由入口，现有业务页面与 API 封装保留在 `src/`。
- `src/app.tsx`：中文组件与消息上下文。
- `src/index.css`：业务单元布局及窄屏适配，不覆盖 Pro 导航和表格主题。
- 生产输出保持 `webui/dist`，继续由 Go `staticfs.go` 嵌入，不改变部署入口。
- `npm ci` 自动运行 `max setup`；`.umi` 与 `.umi-production` 为生成内容，不提交。

后台保留服务器、两类凭据、当前连接、审计、服务设置六个页面。统一自注册 Token 在服务设置中维护。连接错误仅悬停显示完整内容。
