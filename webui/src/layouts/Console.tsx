import { App as AntApp, Button, Spin } from "antd";
import { ProLayout } from "@ant-design/pro-components";
import {
  CloudServerOutlined,
  KeyOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  AuditOutlined,
  LinkOutlined,
  LogoutOutlined,
  CodeOutlined,
} from "@ant-design/icons";
import { useEffect, useState } from "react";
import { Link, Outlet, useLocation } from "@umijs/max";
import { api, authExpiredEvent, type MeResponse } from "../api";
import { Login } from "../Login";
import { ForceChangePassword } from "../ForceChangePassword";
import settings from "../../config/defaultSettings";
const menuRoutes = [
  { path: "/servers", name: "服务器", icon: <CloudServerOutlined /> },
  { path: "/server-credentials", name: "服务器凭据", icon: <KeyOutlined /> },
  {
    path: "/client-credentials",
    name: "客户端凭据",
    icon: <SafetyCertificateOutlined />,
  },
  { path: "/connections", name: "当前连接", icon: <LinkOutlined /> },
  { path: "/audit", name: "审计日志", icon: <AuditOutlined /> },
  { path: "/settings", name: "服务设置", icon: <SettingOutlined /> },
];
export default function Console() {
  const location = useLocation();
  const { message } = AntApp.useApp();
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
    <ProLayout
      {...settings}
      logo={<CodeOutlined />}
      location={location}
      route={{ path: "/", routes: menuRoutes }}
      menu={{ locale: false }}
      menuItemRender={(item, dom) => (
        <Link to={item.path || "/servers"}>{dom}</Link>
      )}
      avatarProps={{
        title: me.username,
        style: { backgroundColor: "#1677ff" },
        children: me.username.slice(0, 1).toUpperCase(),
      }}
      actionsRender={() => [
        <Button
          key="logout"
          type="text"
          icon={<LogoutOutlined />}
          onClick={() =>
            logout().catch(() => message.error("退出失败，请重试"))
          }
        >
          退出
        </Button>,
      ]}
    >
      <Outlet />
    </ProLayout>
  );
}
