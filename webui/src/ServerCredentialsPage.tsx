import { ServerCredentialForm } from "./CredentialForms";
import type { ProColumns } from "@ant-design/pro-components";
import { Empty, Dropdown as ActionDropdown } from "antd";
import { ReloadOutlined as RefreshIcon, MoreOutlined } from "@ant-design/icons";
import { ToolbarIconAction, ListToolbarSearch } from "./ListControls";
import { useListView } from "./useListView";
import { PageContainer, ProTable } from "@ant-design/pro-components";
import { Grid, Button, Tag } from "antd";
import { useFeedback } from "./useFeedback";
import { useEffect, useState } from "react";
import { api, ApiError, type ServerRecord, type ServerCredential } from "./api";
import { ChipList } from "./ChipList";

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
  const [loading, setLoading] = useState(false);
  const screens = Grid.useBreakpoint();
  const { confirm, alert, success } = useFeedback();
  const [creds, setCreds] = useState<ServerCredential[]>([]);
  const [servers, setServers] = useState<ServerRecord[]>([]);
  const [editing, setEditing] = useState<
    (Omit<ServerCredential, "id"> & { id?: number }) | null
  >(null);
  const [error, setError] = useState("");

  async function load() {
    setLoading(true);
    try {
      const [c, r] = await Promise.all([
        api.listServerCredentials(),
        api.listServers(),
      ]);
      setCreds(c ?? []);
      setServers(r ?? []);
      setError("");
    } finally {
      setLoading(false);
    }
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

  async function save(values: Omit<ServerCredential, "id">) {
    if (!editing) return false;
    const payload = { ...editing, ...values };
    setError("");

    // 取消勾选的服务器会失去这份凭证、认证方式变空,需要之后单独重新设置,先提醒一下。
    if (editing.id != null) {
      const before = creds.find((c) => c.id === editing.id);
      const removed = (before?.proxy_users ?? []).filter(
        (ru) => !payload.proxy_users.includes(ru),
      );
      if (removed.length > 0) {
        const ok = await confirm(
          `取消勾选后,${removed.join(", ")} 会失去这份凭证,认证方式变空,需要单独重新设置密码/私钥或换一份凭证,确定继续吗?`,
        );
        if (!ok) return false;
      }
    }

    try {
      if (editing.id != null) {
        await api.updateServerCredential(editing.id, payload);
      } else {
        await api.createServerCredential(payload);
      }
      success(editing.id != null ? "更新成功" : "创建成功");
      await load();
      return true;
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "保存失败");
      return false;
    }
  }

  async function remove(id: number, label: string) {
    if (!(await confirm(`确定删除服务器凭证 "${label}" 吗?`))) return;
    try {
      await api.deleteServerCredential(id);
      await load();
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "删除失败");
    }
  }

  const columns: ProColumns<ServerCredential>[] = [
    {
      title: "名称",
      dataIndex: "label",
      width: 220,
      sorter: (a, b) => a.label.localeCompare(b.label),
    },
    { title: "SSH登录名", dataIndex: "target_user", width: 170 },
    {
      title: "认证方式",
      key: "auth_type",
      width: 120,
      render: (_, c) => (
        <Tag>{c.auth_type === "password" ? "密码" : "私钥"}</Tag>
      ),
    },
    {
      title: "绑定的服务器",
      key: "proxy_users",
      width: 360,
      render: (_, c) => <ChipList items={c.proxy_users} emptyText="暂无关联" />,
    },
    {
      title: "操作",
      key: "option",
      width: 95,
      fixed: screens.md ? "right" : undefined,
      render: (_, c) => (
        <div className="row-actions">
          <Button type="link" onClick={() => startEdit(c)}>
            编辑
          </Button>
          <ActionDropdown
            menu={{
              items: [
                {
                  key: "delete",
                  label: "删除",
                  danger: true,
                  onClick: () => remove(c.id, c.label),
                },
              ],
            }}
          >
            <Button
              type="text"
              size="small"
              aria-label="更多操作"
              icon={<MoreOutlined />}
            />
          </ActionDropdown>
        </div>
      ),
    },
  ];
  const view = useListView(creds, columns, "ServerCredentialsPage", {
    searchText: (credential) => [credential.label, credential.target_user, ...(credential.proxy_users ?? [])].join(" "),
  });

  return (
    <PageContainer
      title={false}
      ghost
      style={{ padding: 0 }}
      breadcrumb={{ items: [{ title: "运维管理" }, { title: "服务器凭证" }] }}
    >
      {error && !editing && (
        <p role="alert" className="mb-3 text-red-600">
          {error}
        </p>
      )}

      <ProTable<ServerCredential>
        rowKey="id"

        {...view.tableProps}
        loading={loading}
        headerTitle={
          <ListToolbarSearch
            value={view.query}
            onSearch={view.search}
            placeholder="搜索名称 / SSH登录名 / 绑定服务器"
          />
        }
        toolBarRender={() => [
          <Button key="create" type="primary" onClick={startCreate}>
            新建
          </Button>,
          <ToolbarIconAction
            key="refresh"
            label="刷新"
            icon={<RefreshIcon />}
            disabled={loading}
            onClick={() => {
              void load().catch(() => setError("刷新失败"));
            }}
          />,
        ]}
        locale={{
          emptyText: (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={
                error
                  ? "加载失败，请刷新重试"
                  : view.hasFilters
                    ? "未找到匹配记录，请修改筛选条件"
                    : "暂无记录"
              }
            />
          ),
        }}
      />

      {editing && (
        <ServerCredentialForm
          key={editing.id ?? "new"}
          initial={editing}
          servers={servers}
          onSave={save}
          onClose={() => setEditing(null)}
        />
      )}
    </PageContainer>
  );
}
