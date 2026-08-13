import { useEffect, useState } from "react";
import { BrowserRouter, Navigate, NavLink, Route, Routes } from "react-router-dom";
import { api, authExpiredEvent, type MeResponse } from "./api";
import { Login } from "./Login";
import { ForceChangePassword } from "./ForceChangePassword";
import { ServersPage } from "./ServersPage";
import { ServerCredentialsPage } from "./ServerCredentialsPage";
import { ClientCredentialsPage } from "./ClientCredentialsPage";
import { SettingsPage } from "./SettingsPage";
import { AuditPage } from "./AuditPage";
import { ConnectionsPage } from "./ConnectionsPage";

const NAV_ITEMS = [
  { path: "/servers", label: "服务器" },
  { path: "/server-credentials", label: "服务器凭据" },
  { path: "/client-credentials", label: "客户端凭据" },
  { path: "/settings", label: "监听设置" },
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
      api.me().then(setMe).catch(() => undefined);
    }, 60_000);
    return () => clearInterval(timer);
  }, [me]);

  async function logout() {
    await api.logout();
    setMe(null);
  }

  if (!checked) return null;

  if (!me) {
    return <Login onLoggedIn={setMe} />;
  }

  if (!me.initialized) {
    return <ForceChangePassword onDone={() => setMe({ ...me, initialized: true })} />;
  }

  return (
    <BrowserRouter>
      <div className="min-h-screen bg-slate-50 dark:bg-slate-900">
        <header className="border-b border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-950">
          <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-2 px-4 py-4 sm:px-6">
            <h1 className="text-lg font-semibold text-slate-900 dark:text-slate-100">claude-ssh-proxy 管理后台</h1>
            <div className="flex items-center gap-4 text-sm text-slate-500 dark:text-slate-400">
              <span>{me.username}</span>
              <button onClick={logout} className="text-indigo-600 hover:underline dark:text-indigo-400">
                退出登录
              </button>
            </div>
          </div>
          <nav className="mx-auto flex max-w-7xl gap-1 overflow-x-auto px-4 sm:px-6">
            {NAV_ITEMS.map(({ path, label }) => (
              <NavLink
                key={path}
                to={path}
                className={({ isActive }) =>
                  `shrink-0 border-b-2 px-3 py-2 text-sm font-medium ${
                    isActive
                      ? "border-indigo-600 text-indigo-600 dark:text-indigo-400"
                      : "border-transparent text-slate-500 hover:text-slate-700 dark:text-slate-400"
                  }`
                }
              >
                {label}
              </NavLink>
            ))}
          </nav>
        </header>

        <main className="mx-auto max-w-7xl px-4 py-6 sm:px-6 sm:py-8">
          <Routes>
            <Route path="/servers" element={<ServersPage />} />
            <Route path="/server-credentials" element={<ServerCredentialsPage />} />
            <Route path="/client-credentials" element={<ClientCredentialsPage />} />
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="/audit" element={<AuditPage />} />
            <Route path="/connections" element={<ConnectionsPage />} />
            <Route path="*" element={<Navigate to="/servers" replace />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  );
}

export default App;
