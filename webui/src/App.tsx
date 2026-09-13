import {
  App as AntApp,
  Avatar,
  Button,
  Drawer,
  Grid,
  Layout,
  Menu,
  Space,
  Spin,
  Typography,
} from "antd";
import {
  CloudServerOutlined,
  KeyOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  AuditOutlined,
  LinkOutlined,
  MenuOutlined,
  LogoutOutlined,
} from "@ant-design/icons";
import { lazy, Suspense, useEffect, useState } from "react";
import {
  BrowserRouter,
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router-dom";
import { api, authExpiredEvent, type MeResponse } from "./api";
import { Login } from "./Login";
import { ForceChangePassword } from "./ForceChangePassword";
const ServersPage = lazy(() =>
  import("./ServersPage").then((module) => ({ default: module.ServersPage })),
);
const ServerCredentialsPage = lazy(() =>
  import("./ServerCredentialsPage").then((module) => ({
    default: module.ServerCredentialsPage,
  })),
);
const ClientCredentialsPage = lazy(() =>
  import("./ClientCredentialsPage").then((module) => ({
    default: module.ClientCredentialsPage,
  })),
);
const SettingsPage = lazy(() =>
  import("./SettingsPage").then((module) => ({ default: module.SettingsPage })),
);
const AuditPage = lazy(() =>
  import("./AuditPage").then((module) => ({ default: module.AuditPage })),
);
const ConnectionsPage = lazy(() =>
  import("./ConnectionsPage").then((module) => ({
    default: module.ConnectionsPage,
  })),
);

const NAV_ITEMS = [
  { path: "/servers", label: "服务器" },
  { path: "/server-credentials", label: "服务器凭据" },
  { path: "/client-credentials", label: "客户端凭据" },
  { path: "/settings", label: "服务设置" },
  { path: "/audit", label: "审计日志" },
  { path: "/connections", label: "当前连接" },
];

function App() {
  const [me, setMe] = useState<MeResponse | null>(null);
  const [checked, setChecked] = useState(false);

  useEffect(() => {
    api
      .me()
      .then(setMe)
      .catch(() => setMe(null))
      .finally(() => setChecked(true));
  }, []);

  useEffect(() => {
    const onAuthExpired = () => setMe(null);
    window.addEventListener(authExpiredEvent, onAuthExpired);
    return () => window.removeEventListener(authExpiredEvent, onAuthExpired);
  }, []);

  useEffect(() => {
    if (!me) return;
    const timer = setInterval(() => {
      api
        .me()
        .then(setMe)
        .catch(() => undefined);
    }, 60_000);
    return () => clearInterval(timer);
  }, [me]);

  async function logout() {
    await api.logout();
    setMe(null);
  }

  if (!checked)
    return (
      <div className="loading-screen">
        <Spin size="large" />
      </div>
    );

  if (!me) {
    return <Login onLoggedIn={setMe} />;
  }

  if (!me.initialized) {
    return (
      <ForceChangePassword onDone={() => setMe({ ...me, initialized: true })} />
    );
  }

  return (
    <BrowserRouter>
      <ConsoleLayout username={me.username} onLogout={logout}>
        <Suspense fallback={<Spin />}>
          <Routes>
            <Route
              path="/agent-enrollments"
              element={<Navigate to="/settings" replace />}
            />
            <Route path="/servers" element={<ServersPage />} />
            <Route
              path="/server-credentials"
              element={<ServerCredentialsPage />}
            />
            <Route
              path="/client-credentials"
              element={<ClientCredentialsPage />}
            />
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="/audit" element={<AuditPage />} />
            <Route path="/connections" element={<ConnectionsPage />} />
            <Route path="*" element={<Navigate to="/servers" replace />} />
          </Routes>
        </Suspense>
      </ConsoleLayout>
    </BrowserRouter>
  );
}

export default App;

const icons = [
  <CloudServerOutlined key="0" />,
  <KeyOutlined key="1" />,
  <SafetyCertificateOutlined key="2" />,
  <SettingOutlined key="3" />,
  <AuditOutlined key="4" />,
  <LinkOutlined key="5" />,
];
function ConsoleLayout({
  username,
  onLogout,
  children,
}: {
  username: string;
  onLogout: () => Promise<void>;
  children: React.ReactNode;
}) {
  const location = useLocation();
  const navigate = useNavigate();
  const screens = Grid.useBreakpoint();
  const [open, setOpen] = useState(false);
  const { message } = AntApp.useApp();
  const title =
    NAV_ITEMS.find((item) => item.path === location.pathname)?.label ||
    "服务器";
  const menu = (
    <Menu
      mode="inline"
      selectedKeys={[location.pathname]}
      items={NAV_ITEMS.map((item, i) => ({
        key: item.path,
        icon: icons[i],
        label: item.label,
      }))}
      onClick={({ key }) => {
        navigate(key);
        setOpen(false);
      }}
    />
  );
  const brand = (
    <div className="brand">
      <CloudServerOutlined />
      <span>
        ops-ssh-proxy<small>运维连接管理</small>
      </span>
    </div>
  );
  return (
    <Layout className="console-layout">
      {screens.lg && (
        <Layout.Sider width={208} theme="light" className="console-sider">
          {brand}
          {menu}
          <div className="sider-note">SSH 代理 · Agent 自注册</div>
        </Layout.Sider>
      )}
      <Drawer
        title="ops-ssh-proxy"
        placement="left"
        open={open}
        onClose={() => setOpen(false)}
        size={240}
        styles={{ body: { padding: 0 } }}
      >
        {menu}
      </Drawer>
      <Layout style={{ minWidth: 0 }}>
        <Layout.Header className="console-header">
          <Space>
            {!screens.lg && (
              <Button
                aria-label="打开导航"
                icon={<MenuOutlined />}
                onClick={() => setOpen(true)}
              />
            )}
            <Typography.Text strong>{title}</Typography.Text>
          </Space>
          <Space>
            <Avatar
              size="small"
              style={{ background: "#e6f4ff", color: "#1677ff" }}
            >
              {username.slice(0, 1).toUpperCase()}
            </Avatar>
            <span>{username}</span>
            <Button
              type="text"
              icon={<LogoutOutlined />}
              onClick={() =>
                onLogout().catch(() => message.error("退出失败，请重试"))
              }
            >
              退出
            </Button>
          </Space>
        </Layout.Header>
        <Layout.Content className="console-content">{children}</Layout.Content>
      </Layout>
    </Layout>
  );
}
