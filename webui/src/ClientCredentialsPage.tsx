import { ClientCredentialForm } from "./CredentialForms";
import type { ProColumns } from "@ant-design/pro-components";
import { Empty, Dropdown as ActionDropdown } from "antd";
import { ReloadOutlined as RefreshIcon, MoreOutlined } from "@ant-design/icons";
import { ToolbarIconAction } from "./ListControls";
import { useListView } from "./useListView";
import { PageContainer, ProTable } from "@ant-design/pro-components";
import { Grid, Button, Tag } from "antd";
import { useFeedback } from "./useFeedback";
import { useEffect, useState } from "react";
import { api, ApiError, type ClientCredential, type ServerRecord } from "./api";
import { ChipList } from "./ChipList";

const emptyCredential: Omit<ClientCredential, "id" | "has_password"> = {
  label: "",
  auth_type: "public_key",
  public_key: "",
  password: "",
  proxy_users: [],
};

export function ClientCredentialsPage() {
  const [loading, setLoading] = useState(false);
  const screens = Grid.useBreakpoint();
  const { confirm, alert, success } = useFeedback();
  const [creds, setCreds] = useState<ClientCredential[]>([]);
  const [servers, setServers] = useState<ServerRecord[]>([]);
  const [editing, setEditing] = useState<
    | (Omit<ClientCredential, "id" | "has_password"> & {
        id?: number;
        has_password?: boolean;
      })
    | null
  >(null);
  const [error, setError] = useState("");

  async function load() {
    setLoading(true);
    try {
      const [c, r] = await Promise.all([
        api.listClientCredentials(),
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

  function startEdit(c: ClientCredential) {
    setEditing({ ...c, password: "" });
    setError("");
  }

  async function save(values: Omit<ClientCredential, "id" | "has_password">) {
    if (!editing) return false;
    const payload = { ...editing, ...values };
    setError("");
    try {
      if (editing.id != null) {
        await api.updateClientCredential(editing.id, payload);
      } else {
        await api.createClientCredential(payload);
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

  const columns: ProColumns<ClientCredential>[] = [
    {
      title: "名称",
      dataIndex: "label",
      width: 220,
      sorter: (a, b) => a.label.localeCompare(b.label),
    },

    {
      title: "认证方式",
      key: "auth_type",
      width: 120,
      render: (_, c) => (
        <Tag>{c.auth_type === "password" ? "密码" : "公钥"}</Tag>
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
                  label: "删除客户端凭据",
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
  const view = useListView(creds, columns, "ClientCredentialsPage", {});

  return (
    <PageContainer
      title={false}
      ghost
      style={{ padding: 0 }}
      breadcrumb={{ items: [{ title: "运维管理" }, { title: "客户端凭据" }] }}
    >
      {error && !editing && (
        <p role="alert" className="mb-3 text-red-600">
          {error}
        </p>
      )}

      <ProTable<ClientCredential>
        rowKey="id"

        {...view.tableProps}
        loading={loading}
        headerTitle={"客户端凭据"}
        toolBarRender={() => [
          <Button key="create" type="primary" onClick={startCreate}>
            新建客户端凭据
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
        <ClientCredentialForm
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
