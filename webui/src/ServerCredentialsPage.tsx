import { Grid, Button, Input, Modal, Table, Tag } from "antd";
import { useFeedback } from "./useFeedback";
import { useEffect, useState } from "react";
import { api, ApiError, type ServerRecord, type ServerCredential } from "./api";
import { ChipList } from "./ChipList";
import { MultiSelectDropdown, SelectDropdown } from "./MultiSelectDropdown";

const emptyCredential: Omit<ServerCredential, "id"> = {
  label: "",
  target_user: "root",
  auth_type: "password",
  auth_password: "",
  auth_private_key: "",
  auth_private_key_passphrase: "",
  proxy_users: [],
};

export function ServerCredentialsPage() {
  const screens = Grid.useBreakpoint();
  const { confirm, alert } = useFeedback();
  const [creds, setCreds] = useState<ServerCredential[]>([]);
  const [servers, setServers] = useState<ServerRecord[]>([]);
  const [editing, setEditing] = useState<
    (Omit<ServerCredential, "id"> & { id?: number }) | null
  >(null);
  const [error, setError] = useState("");

  async function load() {
    const [c, r] = await Promise.all([
      api.listServerCredentials(),
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
    setError("");
  }

  function startEdit(c: ServerCredential) {
    setEditing({
      ...c,
      auth_password: "",
      auth_private_key: "",
      auth_private_key_passphrase: "",
    });
    setError("");
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

    // 取消勾选的服务器会失去这份凭据、认证方式变空,需要之后单独重新设置,先提醒一下。
    if (editing.id != null) {
      const before = creds.find((c) => c.id === editing.id);
      const removed = (before?.proxy_users ?? []).filter(
        (ru) => !editing.proxy_users.includes(ru),
      );
      if (removed.length > 0) {
        const ok = await confirm(
          `取消勾选后,${removed.join(", ")} 会失去这份凭据,认证方式变空,需要单独重新设置密码/私钥或换一份凭据,确定继续吗?`,
        );
        if (!ok) return;
      }
    }

    try {
      if (editing.id != null) {
        await api.updateServerCredential(editing.id, editing);
      } else {
        await api.createServerCredential(editing);
      }
      setEditing(null);
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "保存失败");
    }
  }

  async function remove(id: number, label: string) {
    if (!(await confirm(`确定删除服务器凭据 "${label}" 吗?`))) return;
    try {
      await api.deleteServerCredential(id);
      await load();
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "删除失败");
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
        <h2 className="text-lg font-semibold text-slate-900 ">服务器凭据</h2>
        <Button type="primary" onClick={startCreate}>
          + 添加服务器凭据
        </Button>
      </div>

      <p className="mb-4 text-sm text-slate-500 ">
        多台服务器可以共用同一份凭据。已绑定服务器的凭据不能删除。
      </p>

      <Table<ServerCredential>
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
          { title: "SSH 登录名", dataIndex: "target_user", width: 170 },
          {
            title: "认证方式",
            width: 120,
            render: (_, c) => (
              <Tag>{c.auth_type === "password" ? "密码" : "私钥"}</Tag>
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
            editing.id != null ? `编辑 ${editing.label}` : "添加服务器凭据"
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
              名称(比如"生产环境统一密码")
            </label>
            <Input
              value={editing.label}
              onChange={(e) =>
                setEditing({ ...editing, label: e.target.value })
              }
              autoFocus
            />
          </div>

          <div className="mb-3">
            <label className="mb-1 block text-xs text-slate-500 ">
              SSH登录名(比如 root)
            </label>
            <Input
              value={editing.target_user}
              onChange={(e) =>
                setEditing({ ...editing, target_user: e.target.value })
              }
            />
          </div>

          <div className="mb-3">
            <label className="mb-1 block text-xs text-slate-500 ">
              认证方式
            </label>
            <SelectDropdown
              options={[
                { value: "password", label: "密码" },
                { value: "private_key", label: "私钥" },
              ]}
              value={editing.auth_type}
              onChange={(v) => setEditing({ ...editing, auth_type: v })}
            />
          </div>

          {editing.auth_type === "password" ? (
            <div className="mb-3">
              <label className="mb-1 block text-xs text-slate-500 ">
                {editing.id != null ? "密码(留空则不修改)" : "密码"}
              </label>
              <Input.Password
                value={editing.auth_password}
                onChange={(e) =>
                  setEditing({ ...editing, auth_password: e.target.value })
                }
              />
            </div>
          ) : (
            <>
              <div className="mb-3">
                <label className="mb-1 block text-xs text-slate-500 ">
                  {editing.id != null
                    ? "私钥内容(PEM,留空则不修改)"
                    : "私钥内容(PEM)"}
                </label>
                <Input.TextArea
                  className="h-24 font-mono"
                  value={editing.auth_private_key}
                  onChange={(e) =>
                    setEditing({ ...editing, auth_private_key: e.target.value })
                  }
                />
              </div>
              <div className="mb-3">
                <label className="mb-1 block text-xs text-slate-500 ">
                  私钥密码(如果有)
                </label>
                <Input.Password
                  value={editing.auth_private_key_passphrase}
                  onChange={(e) =>
                    setEditing({
                      ...editing,
                      auth_private_key_passphrase: e.target.value,
                    })
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
