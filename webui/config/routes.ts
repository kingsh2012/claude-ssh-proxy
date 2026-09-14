export default [
  {
    path: "/",
    component: "@/layouts/Console",
    routes: [
      { path: "/servers", component: "@/pages/ServersPage", name: "服务器列表" },
      {
        path: "/server-credentials",
        component: "@/pages/ServerCredentialsPage",
        name: "服务器凭证",
      },
      {
        path: "/client-credentials",
        component: "@/pages/ClientCredentialsPage",
        name: "客户端凭证",
      },
      {
        path: "/connections",
        component: "@/pages/ConnectionsPage",
        name: "当前连接",
      },
      { path: "/audit", component: "@/pages/AuditPage", name: "审计日志" },
      {
        path: "/settings",
        component: "@/pages/SettingsPage",
        name: "服务设置",
      },
      { path: "/agent-enrollments", redirect: "/settings" },
      { path: "/", redirect: "/servers" },
      { path: "*", redirect: "/servers" },
    ],
  },
];
