import { Grid, Button, Input, Modal, Table, Tag } from "antd";
import { useFeedback } from "./useFeedback";
import { useEffect, useState } from "react";
import { api, ApiError, type ClientCredential, type ServerRecord } from "./api";
import { ChipList } from "./ChipList";
import { MultiSelectDropdown, SelectDropdown } from "./MultiSelectDropdown";

const emptyCredential: Omit<ClientCredential, "id" | "has_password"> = {
  label: "",
  auth_type: "public_key",
  public_key: "",
  password: "",
  proxy_users: [],
};

// extractLabelFromPublicKey 取公钥内容里最后一段(comment,比如 "root@vultr")作为默认名称建议。
// authorized_keys 格式是 "类型 base64内容 [comment]",comment 是可选的。
function extractLabelFromPublicKey(publicKey: string): string {
  const parts = publicKey.trim().split(/\s+/);
  return parts.length >= 3 ? parts[parts.length - 1] : "";
}

export function ClientCredentialsPage() {
  const screens = Grid.useBreakpoint();
  const { confirm } = useFeedback();
  const [creds, setCreds] = useState<ClientCredential[]>([]);
  const [servers, setServers] = useState<ServerRecord[]>([]);
  const [editing, setEditing] = useState<
    | (Omit<ClientCredential, "id" | "has_password"> & {
        id?: number;
        has_password?: boolean;
      })
    | null
  >(null);
  const [labelAuto, setLabelAuto] = useState(true);
  const [error, setError] = useState("");

  async function load() {
    const [c, r] = await Promise.all([
      api.listClientCredentials(),
      api.listServers(),
    ]);
    setCreds(c ?? []);
    setServers(r ?? []);
  }

  useEffect(() => {
    load().catch(() => setError("加载失败，请刷新重试"));
  }, []);

  function startCreate() {
    setEditing({ ...emptyCredential });
    setLabelAuto(true);
    setError("");
  }

  function startEdit(c: ClientCredential) {
    setEditing({ ...c, password: "" });
    setLabelAuto(
      c.auth_type === "public_key" &&
        c.label === extractLabelFromPublicKey(c.public_key ?? ""),
    );
    setError("");
  }

  function onPublicKeyChange(value: string) {
    if (!editing) return;
    const derived = labelAuto
      ? extractLabelFromPublicKey(value)
      : editing.label;
    setEditing({ ...editing, public_key: value, label: derived });
  }

  function onLabelChange(value: string) {
    if (!editing) return;
    setLabelAuto(false);
    setEditing({ ...editing, label: value });
  }

  function toggleServer(proxyUser: string) {
    if (!editing) return;
    const set = new Set(editing.proxy_users);
    if (set.has(proxyUser)) {
      set.delete(proxyUser);
    } else {
      set.add(proxyUser);
    }
    setEditing({ ...editing, proxy_users: Array.from(set) });
  }

  async function save() {
    if (!editing) return;
    setError("");
    try {
      if (editing.id != null) {
        await api.updateClientCredential(editing.id, editing);
      } else {
        await api.createClientCredential(editing);
      }
      setEditing(null);
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "保存失败");
    }
  }

  async function remove(id: number, label: string) {
    if (
      !(await confirm(
        `确定删除客户端凭据 "${label}" 吗?删除后所有关联它的服务器都会失去这份凭据的登录权限。`,
      ))
    )
      return;
    try {
      await api.deleteClientCredential(id);
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "删除失败");
    }
  }

  return (
    <div>
      {error && !editing && (
        <p role="alert" className="mb-3 text-red-600">
          {error}
        </p>
      )}
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-lg font-semibold text-slate-900 ">客户端凭据</h2>
        <Button type="primary" onClick={startCreate}>
          + 添加客户端凭据
        </Button>
      </div>

      <p className="mb-4 text-sm text-slate-500 ">
        每份凭据代表一个客户端身份,可以绑定多台服务器。
      </p>

      <Table<ClientCredential>
        rowKey="id"
        size="middle"
        dataSource={creds}
        scroll={{ x: 900 }}
        pagination={{
          defaultPageSize: 20,
          showSizeChanger: true,
          showTotal: (total) => `共 ${total} 份`,
        }}
        columns={[
          {
            title: "名称",
            dataIndex: "label",
            width: 220,
            sorter: (a, b) => a.label.localeCompare(b.label),
          },

          {
            title: "认证方式",
            width: 120,
            render: (_, c) => (
              <Tag>{c.auth_type === "password" ? "密码" : "公钥"}</Tag>
            ),
          },
          {
            title: "绑定的服务器",
            render: (_, c) => (
              <ChipList items={c.proxy_users} emptyText="暂无关联" />
            ),
          },
          {
            title: "操作",
            width: 150,
            fixed: screens.md ? "right" : undefined,
            render: (_, c) => (
              <div className="row-actions">
                <Button type="link" onClick={() => startEdit(c)}>
                  编辑
                </Button>
                <Button
                  type="link"
                  danger
                  onClick={() => remove(c.id, c.label)}
                >
                  删除
                </Button>
              </div>
            ),
          },
        ]}
      />

      {editing && (
        <Modal
          open
          title={
            editing.id != null ? `编辑 ${editing.label}` : "添加客户端凭据"
          }
          onCancel={() => setEditing(null)}
          footer={null}
          width={600}
          destroyOnHidden
          styles={{
            body: { maxHeight: "70vh", overflowY: "auto", paddingTop: 16 },
          }}
        >
          <div className="mb-3">
            <label className="mb-1 block text-xs text-slate-500 ">
              认证方式
            </label>
            <SelectDropdown
              options={[
                { value: "public_key", label: "公钥" },
                { value: "password", label: "密码" },
              ]}
              value={editing.auth_type}
              onChange={(v) => setEditing({ ...editing, auth_type: v })}
            />
          </div>

          {editing.auth_type === "public_key" ? (
            <>
              <div className="mb-3">
                <label className="mb-1 block text-xs text-slate-500 ">
                  公钥内容
                </label>
                <Input.TextArea
                  className="h-20 font-mono"
                  value={editing.public_key}
                  onChange={(e) => onPublicKeyChange(e.target.value)}
                  placeholder="ssh-ed25519 AAAA... claude-client"
                  autoFocus
                />
              </div>
              <div className="mb-3">
                <label className="mb-1 block text-xs text-slate-500 ">
                  名称(默认从公钥末尾的 comment 自动截取,可以手动改)
                </label>
                <Input
                  value={editing.label}
                  onChange={(e) => onLabelChange(e.target.value)}
                />
              </div>
            </>
          ) : (
            <>
              <div className="mb-3">
                <label className="mb-1 block text-xs text-slate-500 ">
                  {editing.has_password ? "密码(已设置,留空则不修改)" : "密码"}
                </label>
                <Input.Password
                  value={editing.password}
                  onChange={(e) =>
                    setEditing({ ...editing, password: e.target.value })
                  }
                  autoFocus
                />
              </div>
              <div className="mb-3">
                <label className="mb-1 block text-xs text-slate-500 ">
                  名称
                </label>
                <Input
                  value={editing.label}
                  onChange={(e) =>
                    setEditing({ ...editing, label: e.target.value })
                  }
                />
              </div>
            </>
          )}

          <div className="mb-3">
            <label className="mb-1 block text-xs text-slate-500 ">
              绑定的服务器(可多选)
            </label>
            <MultiSelectDropdown
              options={servers.map((r) => ({
                id: r.proxy_user,
                label: r.proxy_user,
                sublabel: `(${r.target_host}:${r.target_port})`,
              }))}
              selectedIds={new Set(editing.proxy_users)}
              onToggle={(id) => toggleServer(id as string)}
              placeholder="(未选择)"
              emptyText='还没有配置任何服务器,先去"服务器"页面添加'
            />
          </div>

          {error && <p className="mb-2 text-sm text-red-600 ">{error}</p>}

          <div className="mt-4 flex justify-end gap-2">
            <Button onClick={() => setEditing(null)}>取消</Button>
            <Button type="primary" onClick={save}>
              保存
            </Button>
          </div>
        </Modal>
      )}
    </div>
  );
}
