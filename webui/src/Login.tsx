import { UserOutlined, LockOutlined } from "@ant-design/icons";
import { LoginForm, ProFormText } from "@ant-design/pro-components";
import { useState } from "react";
import { api, ApiError, type MeResponse } from "./api";

export function Login({
  onLoggedIn,
}: {
  onLoggedIn: (me: MeResponse) => void;
}) {
  const [error, setError] = useState("");
  return (
    <div className="auth-screen">
      <LoginForm<{ username: string; password: string }>
        title="ops-ssh-proxy"
        subTitle="运维管理后台"
        submitter={{ searchConfig: { submitText: "登录" } }}
        onFinish={async ({ username, password }) => {
          setError("");
          try {
            onLoggedIn(await api.login(username, password));
            return true;
          } catch (err) {
            setError(err instanceof ApiError ? err.message : "登录失败");
            return false;
          }
        }}
      >
        <ProFormText
          name="username"
          label="用户名"
          fieldProps={{
            prefix: <UserOutlined />,
            autoComplete: "username",
            autoFocus: true,
          }}
          placeholder="请输入用户名"
          rules={[{ required: true, message: "请输入用户名" }]}
        />
        <ProFormText.Password
          name="password"
          label="密码"
          fieldProps={{
            prefix: <LockOutlined />,
            autoComplete: "current-password",
          }}
          placeholder="请输入密码"
          rules={[{ required: true, message: "请输入密码" }]}
        />
        {error && (
          <p role="alert" className="text-red-600">
            {error}
          </p>
        )}
      </LoginForm>
    </div>
  );
}
