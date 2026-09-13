import { Collapse, Card, Input, Button } from "antd";
import { useFeedback } from "./useFeedback";
import { useEffect, useState } from "react";
import {
  api,
  ApiError,
  type ClientCredential,
  type SelfRegistration,
} from "./api";
import { MultiSelectDropdown } from "./MultiSelectDropdown";

export function SelfRegistrationSettings() {
  const { confirm } = useFeedback();
  const [settings, setSettings] = useState<SelfRegistration | null>(null);
  const [address, setAddress] = useState(
    window.location.protocol === "https:"
      ? `wss://${window.location.host}/agent`
      : "",
  );
  const [credentials, setCredentials] = useState<ClientCredential[]>([]);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [advanced, setAdvanced] = useState(false);
  const [visible, setVisible] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  useEffect(() => {
    Promise.all([api.getSelfRegistration(), api.listClientCredentials()])
      .then(([value, creds]) => {
        setSettings(value);
        if (value.server_url) setAddress(value.server_url);
        setSelected(new Set(value.client_credential_ids));
        setCredentials(creds);
      })
      .catch(() => setError("读取自注册设置失败，请刷新重试"));
  }, []);

  async function generate() {
    if (
      settings?.enabled &&
      !(await confirm(
        "重新生成后，旧 Token 立即失效，使用它的 Agent 会断开。需要用新 Token 重新启动，继续？",
      ))
    )
      return;
    setBusy(true);
    setError("");
    setMessage("");
    try {
      setSettings(
        await api.putSelfRegistration({
          server_url: address,
          client_credential_ids: [...selected],
        }),
      );
      setVisible(false);
      setAdvanced(false);
      setMessage("自注册 Token 已生效");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "保存失败");
    } finally {
      setBusy(false);
    }
  }
  async function disable() {
    if (
      !(await confirm(
        "停用后，使用此 Token 的 Agent 会断开，并且无法注册或重连。继续？",
      ))
    )
      return;
    setBusy(true);
    setError("");
    setMessage("");
    try {
      await api.disableSelfRegistration();
      setSettings((previous) =>
        previous ? { ...previous, enabled: false } : previous,
      );
      setVisible(false);
      setMessage("自注册已停用");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "停用失败");
    } finally {
      setBusy(false);
    }
  }
  async function copy(value: string) {
    setError("");
    setMessage("");
    try {
      if (navigator.clipboard && window.isSecureContext)
        await navigator.clipboard.writeText(value);
      else {
        const field = document.createElement("textarea");
        field.value = value;
        field.style.position = "fixed";
        field.style.opacity = "0";
        document.body.appendChild(field);
        try {
          field.select();
          if (!document.execCommand("copy")) throw new Error("copy");
        } finally {
          field.remove();
        }
      }
      setMessage("已复制");
    } catch {
      setError("复制失败，请点击显示后手动复制");
    }
  }
  const command = `.\\ops-ssh-agent.exe -token '${settings?.token ?? ""}'`;
  return (
    <Card className="registration-card">
      <div className="space-y-3">
        <div className="flex flex-wrap items-center gap-3">
          <h3 className="text-base font-semibold">自注册 Token</h3>
          <span className="text-xs text-slate-500">
            {settings ? (settings.enabled ? "已启用" : "未启用") : "加载中…"}
          </span>
        </div>
        <p className="text-sm text-slate-500">
          多台 Windows 共用此密钥，启动 Agent
          后自动加入服务器列表，无需逐台申请。
        </p>
        {settings?.token && (
          <div className="space-y-2">
            <label htmlFor="registration-token" className="block text-sm">
              当前 Token
            </label>
            <Input
              id="registration-token"
              className="font-mono text-xs"
              type={visible ? "text" : "password"}
              readOnly
              value={settings.token}
              onFocus={(e) => e.currentTarget.select()}
            />
            <div className="flex flex-wrap gap-3 text-sm text-indigo-600 ">
              <Button type="link" onClick={() => setVisible(!visible)}>
                {visible ? "隐藏" : "显示"}
              </Button>
              <Button type="link" onClick={() => copy(settings.token)}>
                复制 Token
              </Button>
              <Button type="link" onClick={() => copy(command)}>
                复制启动命令
              </Button>
            </div>
            {visible && (
              <Input.TextArea
                aria-label="Agent 启动命令"
                className="h-28 font-mono text-xs"
                readOnly
                value={command}
                onFocus={(e) => e.currentTarget.select()}
              />
            )}
          </div>
        )}
        <p className="text-sm text-slate-500">
          默认使用 Windows 主机名作为代理登录名，也可指定：
        </p>
        <pre className="agent-command">{`.\\ops-ssh-agent.exe -token '自注册Token' -hostname 'es-windows-01'`}</pre>
        <Collapse
          ghost
          destroyOnHidden
          activeKey={!settings?.token || advanced ? ["connection"] : []}
          onChange={(keys) => setAdvanced(keys.includes("connection"))}
          items={[
            {
              key: "connection",
              label: "连接地址与默认访问权限",
              children: (
                <div className="space-y-2">
                  <label
                    htmlFor="registration-address"
                    className="block text-sm"
                  >
                    Agent 连接地址
                  </label>
                  <Input
                    id="registration-address"
                    value={address}
                    onChange={(e) => setAddress(e.target.value)}
                    placeholder="wss://proxy.example.com/agent"
                  />
                  <p className="text-xs text-slate-500">
                    填写 Windows 可访问的 WSS 地址，生成 Token
                    时保存。修改后需重新生成 Token。
                  </p>
                  <label className="block text-sm">
                    新主机的默认客户端凭据
                  </label>
                  <MultiSelectDropdown
                    options={credentials.map((c) => ({
                      id: c.id,
                      label: c.label,
                    }))}
                    selectedIds={selected}
                    onToggle={(id) =>
                      setSelected((previous) => {
                        const next = new Set(previous);
                        if (next.has(Number(id))) next.delete(Number(id));
                        else next.add(Number(id));
                        return next;
                      })
                    }
                    placeholder="选择可访问新主机的凭据"
                    emptyText="请先添加客户端凭据"
                  />
                  <p className="text-xs text-slate-500">
                    仅在新主机首次注册时应用，已有主机的权限在服务器列表中管理。
                  </p>
                </div>
              ),
            },
          ]}
        />
        <div className="flex flex-wrap gap-3">
          <Button
            type="primary"
            onClick={generate}
            disabled={!settings || busy || !address || selected.size === 0}
          >
            {busy
              ? "处理中…"
              : settings?.token
                ? "重新生成 Token"
                : "生成 Token"}
          </Button>
          {settings?.enabled && (
            <Button danger type="link" onClick={disable} disabled={busy}>
              停用自注册
            </Button>
          )}
        </div>
        {message && <p className="text-sm text-emerald-600">{message}</p>}
        {error && (
          <p role="alert" className="text-sm text-red-600">
            {error}
          </p>
        )}
      </div>
    </Card>
  );
}
