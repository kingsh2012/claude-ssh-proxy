import { CodeOutlined } from "@ant-design/icons";
import { Input, Button } from "antd";
import { useState } from "react";
import { api, ApiError } from "./api";

export function ForceChangePassword({ onDone }: { onDone: () => void }) {
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    if (newPassword !== confirmPassword) {
      setError("两次输入的新密码不一致");
      return;
    }
    if (newPassword.length < 8) {
      setError("新密码至少 8 位");
      return;
    }
    setLoading(true);
    try {
      await api.changePassword("", newPassword);
      onDone();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "修改失败");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="auth-screen">
      <form onSubmit={handleSubmit} className="auth-card">
        <div className="auth-symbol">
          <CodeOutlined />
        </div>
        <h1 className="mb-2 text-xl font-semibold text-slate-900 ">
          首次登录,请修改密码
        </h1>
        <p className="mb-6 text-sm text-slate-500 ">
          当前账号还在使用初始密码,必须先修改密码才能继续使用管理后台。
        </p>

        <label className="mb-1 block text-sm text-slate-600 ">
          新密码(至少 8 位)
        </label>
        <Input.Password
          className="mb-4 w-full"
          value={newPassword}
          onChange={(e) => setNewPassword(e.target.value)}
          autoFocus
        />
        <label className="mb-1 block text-sm text-slate-600 ">确认新密码</label>
        <Input.Password
          className="mb-4 w-full"
          value={confirmPassword}
          onChange={(e) => setConfirmPassword(e.target.value)}
        />

        {error && <p className="mb-4 text-sm text-red-600 ">{error}</p>}

        <Button
          type="primary"
          htmlType="submit"
          disabled={loading}
          className="w-full"
        >
          {loading ? "提交中..." : "修改密码并继续"}
        </Button>
      </form>
    </div>
  );
}
