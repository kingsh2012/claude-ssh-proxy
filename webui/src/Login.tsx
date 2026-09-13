import { Input, Button } from "antd";
import { useState } from "react";
import { api, ApiError, type MeResponse } from "./api";

export function Login({
  onLoggedIn,
}: {
  onLoggedIn: (me: MeResponse) => void;
}) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setLoading(true);
    try {
      const res = await api.login(username, password);
      onLoggedIn(res);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "登录失败");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-slate-100 ">
      <form
        onSubmit={handleSubmit}
        className="w-full max-w-sm rounded-xl border border-slate-200 bg-white p-8 shadow-sm  "
      >
        <h1 className="mb-6 text-xl font-semibold text-slate-900 ">
          ops-ssh-proxy 管理后台
        </h1>
        <label className="mb-1 block text-sm text-slate-600 ">用户名</label>
        <Input
          className="mb-4 w-full"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          autoFocus
        />
        <label className="mb-1 block text-sm text-slate-600 ">密码</label>
        <Input.Password
          className="mb-4 w-full"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        {error && <p className="mb-4 text-sm text-red-600 ">{error}</p>}
        <Button
          type="primary"
          htmlType="submit"
          disabled={loading}
          className="w-full"
        >
          {loading ? "登录中..." : "登录"}
        </Button>
      </form>
    </div>
  );
}
