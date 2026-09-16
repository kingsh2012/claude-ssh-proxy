import { ServerForm, type ServerFormValues } from "./ServerForm";
import type { ProColumns } from "@ant-design/pro-components";
import { Empty } from "antd";
import {
  ReloadOutlined as RefreshIcon,
  DownOutlined,
  ClearOutlined,
  MoreOutlined,
} from "@ant-design/icons";
import { ToolbarIconAction, ListToolbarSearch, TextColumnFilter } from "./ListControls";
import { useListView } from "./useListView";
import { PageContainer, ProTable } from "@ant-design/pro-components";

import {
  Grid,
  Badge,
  Button,
  Dropdown,
  Input,
  Modal,
  Radio,
  Select,
  Space,
  Tag,
  Typography,
} from "antd";
import { useFeedback } from "./useFeedback";
import { useEffect, useState } from "react";
import {
  api,
  ApiError,
  type ClientCredential,
  type ServerRecord,
  type ServerCredential,
  type ServerOwnership,
} from "./api";
import { ChipList } from "./ChipList";

import { Tooltip } from "./Tooltip";

const emptyServer: ServerRecord = {
  connection_type: "ssh",
  id: 0,
  proxy_user: "",
  target_host: "",
  target_port: 22,
  ownership: "",
  remark: "",
  route_mode: "fixed",
  port_min: 1,
  port_max: 65535,
  enabled: true,
  legacy_algorithms: false,
  host_key_fingerprint: "",
  client_credential_labels: [],
  last_test_at: null,
  last_test_ok: null,
  server_credential_id: null,
};

const ownershipLabels: Record<ServerOwnership, string> = {
  "": "未分类",
  pve_vm: "PVE",
  physical: "物理机",
  cloud: "云服务器",
  network_device: "网络设备",
};

const ownershipColors: Record<Exclude<ServerOwnership, "">, string> = {
  pve_vm: "cyan",
  physical: "blue",
  cloud: "gold",
  network_device: "green",
};

type BulkCredentialKind = "server" | "client";
type BulkCredentialOperation = "replace" | "add" | "remove";

// reconcileClientCredentials 把"这个代理登录名应该关联哪些客户端凭证"落地成实际的 API 调用:
// 对比每份客户端凭证当前的 proxy_users 和期望的勾选结果,只在有变化的凭证上调用更新接口。
// save() 和 CSV 导入(逐行调用)都用这个函数,保证行为一致。
//
// 注意:成功调用后会把 c.proxy_users 就地更新——批量导入时如果好几行服务器共用同一份
// 客户端凭证,必须让后面几行看到前面几行刚写入的关联,否则会互相覆盖,只有最后一行生效。
async function reconcileClientCredentials(
  clientCredentials: ClientCredential[],
  proxyUser: string,
  selectedIds: Set<number>,
) {
  for (const c of clientCredentials) {
    const shouldHave = selectedIds.has(c.id);
    const currentlyHas = c.proxy_users.includes(proxyUser);
    if (shouldHave === currentlyHas) continue;
    const { id, has_password, ...rest } = c;
    void id;
    void has_password;
    const proxyUsers = shouldHave
      ? [...c.proxy_users, proxyUser]
      : c.proxy_users.filter((ln) => ln !== proxyUser);
    await api.updateClientCredential(c.id, {
      ...rest,
      proxy_users: proxyUsers,
    });
    c.proxy_users = proxyUsers;
  }
}

export function ServersPage() {
  const [loading, setLoading] = useState(false);
  const screens = Grid.useBreakpoint();
  const { confirm, alert, success } = useFeedback();
  const [servers, setServers] = useState<ServerRecord[]>([]);
  const [serverCredentials, setServerCredentials] = useState<
    ServerCredential[]
  >([]);
  const [clientCredentials, setClientCredentials] = useState<
    ClientCredential[]
  >([]);
  const [editing, setEditing] = useState<ServerRecord | null>(null);
  const [selectedCredentialIds, setSelectedCredentialIds] = useState<
    Set<number>
  >(new Set());
  const [error, setError] = useState("");
  const [isNew, setIsNew] = useState(false);
  const [testingServer, setTestingServer] = useState<string | null>(null);
  const [testingAll, setTestingAll] = useState(false);
  const [importing, setImporting] = useState(false);
  const [selectedServerIds, setSelectedServerIds] = useState<number[]>([]);
  const [bulkBusy, setBulkBusy] = useState(false);
  const [bulkCredentialKind, setBulkCredentialKind] =
    useState<BulkCredentialKind | null>(null);
  const [bulkCredentialOperation, setBulkCredentialOperation] =
    useState<BulkCredentialOperation>("replace");
  const [bulkServerCredentialID, setBulkServerCredentialID] = useState<
    number | undefined
  >();
  const [bulkClientCredentialIDs, setBulkClientCredentialIDs] = useState<
    number[]
  >([]);

  async function bulkAction(action: "disable" | "enable" | "delete") {
    if (bulkBusy) return;
    const targets = servers.filter((server) => selectedServerIdsInView.includes(server.id));
    if (!targets.length) return;
    const label = action === "delete" ? "删除" : action === "enable" ? "解禁" : "禁用";
    setBulkBusy(true);
    try {
      if (!(await confirm(`确定${label}选中的 ${targets.length} 台服务器吗？${action === "delete" ? "删除后不可在页面恢复。" : action === "enable" ? "已启用的服务器将跳过。" : "已禁用的服务器将跳过。"}\n${targets.map((server) => server.proxy_user).join("、")}`))) return;
      const failed: number[] = [];
      const failures: string[] = [];
      for (const server of targets) {
        try {
          if (action === "delete") {
            await api.deleteServer(server.proxy_user);
            setServers((previous) => previous.filter((row) => row.id !== server.id));
            setClientCredentials((previous) => previous.map((credential) => ({
              ...credential, proxy_users: credential.proxy_users.filter((name) => name !== server.proxy_user),
            })));
          } else if (server.enabled !== (action === "enable")) {
            const updated = await api.setServerEnabled(server.proxy_user, action === "enable");
            setServers((previous) => previous.map((row) => row.id === server.id ? updated : row));
          }
        } catch (err) {
          failed.push(server.id);
          failures.push(`${server.proxy_user}：${err instanceof ApiError ? err.message : "请求失败，请重试"}`);
        }
      }
      setSelectedServerIds(failed);
      const completed = targets.length - failed.length;
      if (completed) success(`${completed} 台服务器已${label}`);
      setError(failures.length ? `${failed.length} 台${label}失败：${failures.join("；")}` : "");
    } finally {
      setBulkBusy(false);
    }
  }

  function openBulkCredential(kind: BulkCredentialKind) {
    if (kind === "server") {
      const sshCount = servers.filter(
        (server) =>
          selectedServerIdsInView.includes(server.id) &&
          server.connection_type !== "agent",
      ).length;
      if (!sshCount) {
        alert("所选服务器中没有SSH服务器");
        return;
      }
    }
    setBulkCredentialOperation("replace");
    setBulkServerCredentialID(undefined);
    setBulkClientCredentialIDs([]);
    setBulkCredentialKind(kind);
  }

  async function submitBulkCredential() {
    if (!bulkCredentialKind || bulkBusy) return;
    const serverIDs = selectedServerIdsInView.filter((id) =>
      bulkCredentialKind === "client"
        ? true
        : servers.some(
            (server) =>
              server.id === id && server.connection_type !== "agent",
          ),
    );
    if (!serverIDs.length) return;
    if (
      bulkCredentialKind === "server" &&
      bulkCredentialOperation === "replace" &&
      bulkServerCredentialID == null
    ) {
      alert("请选择服务器凭证");
      return;
    }
    if (
      bulkCredentialKind === "client" &&
      bulkClientCredentialIDs.length === 0
    ) {
      alert("请选择客户端凭证");
      return;
    }

    setBulkBusy(true);
    try {
      if (bulkCredentialKind === "server") {
        await api.bulkUpdateServerCredential({
          server_ids: serverIDs,
          server_credential_id: bulkServerCredentialID,
          operation:
            bulkCredentialOperation === "remove" ? "remove" : "replace",
        });
      } else {
        await api.bulkUpdateClientCredentials({
          server_ids: serverIDs,
          client_credential_ids: bulkClientCredentialIDs,
          operation: bulkCredentialOperation,
        });
      }
      success(
        bulkCredentialKind === "server"
          ? `${serverIDs.length}台SSH服务器的服务器凭证已批量修改`
          : `${serverIDs.length}台服务器的客户端凭证已批量修改`,
      );
      setBulkCredentialKind(null);
      setSelectedServerIds([]);
      await load();
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "批量修改失败");
    } finally {
      setBulkBusy(false);
    }
  }

  async function load() {
    setLoading(true);
    try {
      const [s, sc, cc] = await Promise.all([
        api.listServers(),
        api.listServerCredentials(),
        api.listClientCredentials(),
      ]);
      setServers(s ?? []);
      setSelectedServerIds((ids) => ids.filter((id) => (s ?? []).some((server) => server.id === id)));
      setError("");
      setServerCredentials(sc ?? []);
      setClientCredentials(cc ?? []);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load().catch(() => setError("加载失败，请刷新重试"));
  }, []);

  async function testOne(proxyUser: string) {
    setTestingServer(proxyUser);
    try {
      const updated = await api.testServer(proxyUser);
      setServers((prev) =>
        prev.map((s) => (s.proxy_user === proxyUser ? updated : s)),
      );
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "测试失败");
    } finally {
      setTestingServer(null);
    }
  }

  async function testAll() {
    setTestingAll(true);
    try {
      const updated = await api.testAllServers();
      setServers(updated ?? []);
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "测试失败");
    } finally {
      setTestingAll(false);
    }
  }

  async function testSelected() {
    const serverIDs = selectedServerIdsInView.filter((id) =>
      servers.some(
        (server) => server.id === id && server.route_mode !== "dynamic_port",
      ),
    );
    if (!serverIDs.length) {
      alert("选中的服务器没有可测试的固定目标");
      return;
    }
    setBulkBusy(true);
    try {
      const updated = await api.testServers(serverIDs);
      const byID = new Map(updated.map((server) => [server.id, server]));
      setServers((previous) =>
        previous.map((server) => byID.get(server.id) ?? server),
      );
      success(`${updated.length} 台服务器测试完成`);
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "批量测试失败");
    } finally {
      setBulkBusy(false);
    }
  }

  async function toggleEnabled(s: ServerRecord) {
    try {
      const updated = await api.setServerEnabled(s.proxy_user, !s.enabled);
      setServers((prev) =>
        prev.map((x) => (x.proxy_user === s.proxy_user ? updated : x)),
      );
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "操作失败");
    }
  }

  function credentialIdsForServer(proxyUser: string): Set<number> {
    return new Set(
      clientCredentials
        .filter((c) => c.proxy_users.includes(proxyUser))
        .map((c) => c.id),
    );
  }

  function startCreate() {
    setEditing({ ...emptyServer });
    setSelectedCredentialIds(new Set());
    setIsNew(true);
    setError("");
  }

  function startEdit(s: ServerRecord) {
    setEditing({ ...s });
    setSelectedCredentialIds(credentialIdsForServer(s.proxy_user));
    setIsNew(false);
    setError("");
  }

  function duplicate(s: ServerRecord) {
    setEditing({ ...s, id: 0 });
    setSelectedCredentialIds(credentialIdsForServer(s.proxy_user));
    setIsNew(true);
    setError("");
  }

  async function save(values: ServerFormValues) {
    if (!editing) return false;
    const { credentialIds, ...fields } = values;
    const payload = {
      ...editing,
      ...fields,
      server_credential_id: fields.server_credential_id ?? null,
    };
    setError("");
    try {
      await api.upsertServer(payload);
      await reconcileClientCredentials(
        clientCredentials,
        payload.proxy_user,
        new Set(credentialIds),
      );
      success(isNew ? "创建成功" : "更新成功");
      await load();
      return true;
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "保存失败");
      return false;
    }
  }

  async function remove(proxyUser: string) {
    if (!(await confirm(`确定删除服务器 ${proxyUser} 吗?`))) return;
    await api.deleteServer(proxyUser);
    await load();
  }

  const columns: ProColumns<ServerRecord>[] = [
    {
      title: "主机 / 代理登录名",
      dataIndex: "proxy_user",
      filterDropdown: TextColumnFilter,
      width: 280,
      sorter: (a, b) => a.proxy_user.localeCompare(b.proxy_user),
      render: (_, s) => (
        <div className="host-cell">
          <div className="host-identity">
            <Typography.Text
              strong
              ellipsis={{ tooltip: s.proxy_user }}
              style={{ maxWidth: "100%" }}
            >
              {s.proxy_user}
            </Typography.Text>
            <div className="host-address">
              {s.connection_type === "agent"
                ? s.target_host
                : `${s.target_host}:${s.route_mode === "dynamic_port" ? "${PORT}" : s.target_port}`}
            </div>
          </div>
        </div>
      ),
    },
    {
      title: "归属",
      dataIndex: "ownership",
      filters: [
        { text: "PVE", value: "pve_vm" },
        { text: "物理机", value: "physical" },
        { text: "云服务器", value: "cloud" },
        { text: "网络设备", value: "network_device" },
        { text: "未分类", value: "unclassified" },
      ],
      width: 120,
      sorter: (a, b) => a.ownership.localeCompare(b.ownership),
      render: (_, s) =>
        s.ownership ? (
          <Tag color={ownershipColors[s.ownership]}>{ownershipLabels[s.ownership]}</Tag>
        ) : (
          <Typography.Text type="secondary">未分类</Typography.Text>
        ),
    },
    {
      title: "备注",
      dataIndex: "remark",
      filterDropdown: TextColumnFilter,
      width: 240,
      sorter: (a, b) => a.remark.localeCompare(b.remark),
      render: (_, s) =>
        s.remark ? (
          <Typography.Text ellipsis={{ tooltip: s.remark }} style={{ maxWidth: "100%" }}>
            {s.remark}
          </Typography.Text>
        ) : (
          <Typography.Text type="secondary">-</Typography.Text>
        ),
    },
    {
      title: "接入方式",
      dataIndex: "connection_type",
      filters: [
        { text: "SSH", value: "ssh" },
        { text: "Agent", value: "agent" },
      ],
      width: 130,
      render: (_, s) => (
        <Tooltip
          text={
            s.connection_type === "agent"
              ? "Agent主动连接 · Token认证"
              : `${s.legacy_algorithms ? "兼容旧设备" : "现代算法"} · ${s.host_key_fingerprint ? "Host Key已校验" : "Host Key未校验"}${s.route_mode === "dynamic_port" ? ` · 端口 ${s.port_min}–${s.port_max}` : ""}`
          }
        >
          <Tag className="connection-tag">
            {s.connection_type === "agent"
              ? "Agent"
              : s.route_mode === "dynamic_port"
                ? "SSH动态端口"
                : "SSH"}
          </Tag>
        </Tooltip>
      ),
    },
    {
      title: "状态",
      dataIndex: "enabled",
      filters: [
        { text: "已启用", value: "true" },
        { text: "已禁用", value: "false" },
      ],
      width: 150,
      render: (_, s) => (
        <div className="server-status">
          <Tag color={s.enabled ? "success" : "error"} className="server-enabled-tag">
            {s.enabled ? "已启用" : "已禁用"}
          </Tag>
          {s.enabled && s.connection_type === "agent" && (
            <Badge status={s.agent_online ? "success" : "warning"} text={s.agent_online ? "在线" : "离线"} />
          )}
        </div>
      ),
    },
    {
      title: "最近验证",
      dataIndex: "last_test_ok",
      filters: [
        { text: "成功", value: "true" },
        { text: "失败", value: "false" },
      ],
      width: 160,
      render: (_, s) =>
        s.route_mode === "dynamic_port" ? (
          <Typography.Text type="secondary">按实际端口测试</Typography.Text>
        ) : (
          <TestStatus server={s} />
        ),
    },
    {
      title: "访问凭证",
      key: "credentials",
      filterDropdown: TextColumnFilter,
      width: 270,
      render: (_, s) => (
        <div className="credentials-cell">
          <div className="credential-line">
            <span className="credential-caption">客户端</span>
            <ChipList
              items={s.client_credential_labels ?? []}
              emptyText="未关联"
              max={2}
            />
          </div>
          {s.connection_type !== "agent" && (
            <div className="credential-line">
              <span className="credential-caption">服务器</span>
              <span className="credential-server">
                {s.server_credential_id != null
                  ? `${s.server_credential_label} · ${s.target_user}`
                  : "未设置"}
              </span>
            </div>
          )}
        </div>
      ),
    },
    {
      title: "操作",
      key: "option",
      width: 270,
      fixed: screens.md ? "right" : undefined,
      render: (_, s) => (
        <div className="row-actions">
          <Button type="link" size="small" disabled={bulkBusy} onClick={() => startEdit(s)}>
            编辑
          </Button>
          <Button
            type="link"
            size="small"
            disabled={bulkBusy || s.route_mode === "dynamic_port" || testingAll}
            loading={testingServer === s.proxy_user}
            onClick={() => testOne(s.proxy_user)}
          >
            测试
          </Button>
          <Button type="link" size="small" disabled={bulkBusy} onClick={() => void toggleEnabled(s)}>
            {s.enabled ? "禁用" : "启用"}
          </Button>
          {s.connection_type !== "agent" && (
            <Button type="link" size="small" disabled={bulkBusy} onClick={() => duplicate(s)}>复制</Button>
          )}
          <Dropdown
            disabled={bulkBusy}
            menu={{
              items: [{
                key: "delete",
                label: "删除",
                danger: true,
                disabled: bulkBusy,
                onClick: () => void remove(s.proxy_user).catch(() => alert("删除失败")),
              }],
            }}
          >
            <Button
              type="text"
              size="small"
              disabled={bulkBusy}
              aria-label="更多操作"
              icon={<MoreOutlined />}
            />
          </Dropdown>
        </div>
      ),
    },
  ];
  const view = useListView(servers, columns, "ServersPage", {
    searchText: (s) =>
      [s.proxy_user, s.target_host, ownershipLabels[s.ownership], s.remark].join(" "),
    match: (s, key, value) =>
      key === "connection_type"
        ? (s.connection_type || "ssh") === value
        : key === "proxy_user"
          ? `${s.proxy_user} ${s.target_host}`.toLowerCase().includes(value.toLowerCase())
        : key === "ownership"
          ? value === "unclassified"
            ? !s.ownership
            : s.ownership === value
        : key === "remark"
          ? s.remark.toLowerCase().includes(value.toLowerCase())
        : key === "credentials"
          ? [
              ...(s.client_credential_labels ?? []),
              s.server_credential_label ?? "",
              s.target_user ?? "",
            ]
              .join(" ")
              .toLowerCase()
              .includes(value.toLowerCase())
        : key === "enabled"
          ? String(s.enabled) === value
          : String(s.last_test_ok) === value,
  });
  const visibleServerIDKey = view.filteredRows.map((server) => server.id).join(",");
  const visibleServerIDs = new Set(view.filteredRows.map((server) => server.id));
  const selectedServerIdsInView = selectedServerIds.filter((id) => visibleServerIDs.has(id));

  useEffect(() => {
    const visibleIDs = new Set(
      visibleServerIDKey ? visibleServerIDKey.split(",").map(Number) : [],
    );
    setSelectedServerIds((previous) => {
      const next = previous.filter((id) => visibleIDs.has(id));
      return next.length === previous.length ? previous : next;
    });
  }, [visibleServerIDKey]);

  return (
    <PageContainer
      title={false}
      ghost
      style={{ padding: 0 }}
      breadcrumb={{ items: [{ title: "运维管理" }, { title: "服务器列表" }] }}
    >
      {error && !editing && (
        <p role="alert" className="mb-3 text-red-600">
          {error}
        </p>
      )}

      <ProTable<ServerRecord>
        rowKey="id"
        className="servers-table"

        {...view.tableProps}
        rowSelection={{
          selectedRowKeys: selectedServerIdsInView,
          preserveSelectedRowKeys: false,
          onChange: (keys) =>
            setSelectedServerIds(
              keys.map(Number).filter((id) => visibleServerIDs.has(id)),
            ),
          getCheckboxProps: () => ({ disabled: bulkBusy }),
          columnWidth: 32,
        }}
        scroll={{ x: Number(view.tableProps.scroll.x) + 32 }}
        tableAlertRender={false}
        tableAlertOptionRender={false}
        rowClassName={(server) => server.enabled ? "" : "server-row-disabled"}
        loading={loading || bulkBusy}
        headerTitle={
          <ListToolbarSearch
            value={view.query}
            onSearch={view.search}
            placeholder="搜索主机、地址、归属或备注"
          />
        }
        toolBarRender={() => [
          <Dropdown
            key="bulk"
            trigger={["click"]}
            disabled={bulkBusy || selectedServerIdsInView.length === 0}
            menu={{
              items: [
                { key: "serverCredential", label: "批量修改服务器凭证" },
                { key: "clientCredentials", label: "批量修改客户端凭证" },
                { type: "divider" },
                { key: "test", label: "批量测试" },
                { key: "disable", label: "批量禁用" },
                { key: "enable", label: "批量启用" },
                { key: "delete", label: "批量删除", danger: true },
              ],
              onClick: ({ key }) => {
                if (key === "serverCredential") {
                  openBulkCredential("server");
                } else if (key === "clientCredentials") {
                  openBulkCredential("client");
                } else if (key === "test") {
                  void testSelected();
                } else {
                  void bulkAction(key as "disable" | "enable" | "delete");
                }
              },
            }}
          >
            <Button disabled={bulkBusy || selectedServerIdsInView.length === 0}>
              批量操作{selectedServerIdsInView.length > 0 ? ` (${selectedServerIdsInView.length})` : ""} <DownOutlined />
            </Button>
          </Dropdown>,
          <Button key="create" disabled={bulkBusy} type="primary" onClick={startCreate}>
            新建
          </Button>,
          <Button disabled={bulkBusy} key="import" onClick={() => setImporting(true)}>
            导入
          </Button>,
          <Button
            key="test"
            onClick={testAll}
            loading={testingAll}
            disabled={bulkBusy || !servers.length}
          >
            测试全部
          </Button>,
          <ToolbarIconAction
            key="refresh"
            label="刷新"
            icon={<RefreshIcon />}
            disabled={loading || bulkBusy}
            onClick={() => {
              void load().catch(() => alert("刷新失败"));
            }}
          />,
          <ToolbarIconAction
            key="clear"
            label="清除筛选"
            icon={<ClearOutlined />}
            disabled={!view.hasFilters}
            onClick={view.reset}
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
        <ServerForm
          key={isNew ? "new" : editing.id}
          initial={{ ...editing, credentialIds: [...selectedCredentialIds] }}
          isNew={isNew}
          serverCredentials={serverCredentials}
          clientCredentials={clientCredentials}
          onSave={save}
          onClose={() => setEditing(null)}
        />
      )}

      {importing && (
        <ImportModal
          servers={servers}
          serverCredentials={serverCredentials}
          clientCredentials={clientCredentials}
          onClose={() => setImporting(false)}
          onDone={load}
        />
      )}

      <Modal
        title={
          bulkCredentialKind === "server"
            ? "批量修改服务器凭证"
            : "批量修改客户端凭证"
        }
        open={bulkCredentialKind !== null}
        okText="确定"
        cancelText="取消"
        confirmLoading={bulkBusy}
        onOk={() => void submitBulkCredential()}
        onCancel={() => !bulkBusy && setBulkCredentialKind(null)}
      >
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          <Typography.Text type="secondary">
            已选择{selectedServerIdsInView.length}台服务器
            {bulkCredentialKind === "server" &&
              servers.some(
                (server) =>
                  selectedServerIdsInView.includes(server.id) &&
                  server.connection_type === "agent",
              ) && "，其中Agent服务器将跳过"}
          </Typography.Text>
          <Radio.Group
            value={bulkCredentialOperation}
            onChange={(event) =>
              setBulkCredentialOperation(event.target.value)
            }
          >
            <Space direction="vertical">
              <Radio value="replace">覆盖</Radio>
              {bulkCredentialKind === "client" && (
                <Radio value="add">新增</Radio>
              )}
              <Radio value="remove">删除</Radio>
            </Space>
          </Radio.Group>
          {bulkCredentialKind === "server" ? (
            bulkCredentialOperation !== "remove" ? (
              <Select
                value={bulkServerCredentialID}
                onChange={setBulkServerCredentialID}
                placeholder="请选择服务器凭证"
                style={{ width: "100%" }}
                options={serverCredentials.map((credential) => ({
                  value: credential.id,
                  label: `${credential.label}（${credential.target_user}）`,
                }))}
              />
            ) : null
          ) : (
            <Select
              mode="multiple"
              value={bulkClientCredentialIDs}
              onChange={setBulkClientCredentialIDs}
              placeholder="请选择客户端凭证"
              style={{ width: "100%" }}
              options={clientCredentials.map((credential) => ({
                value: credential.id,
                label: credential.label,
              }))}
            />
          )}
          <Typography.Text type="secondary">
            {bulkCredentialOperation === "replace"
              ? bulkCredentialKind === "server"
                ? "使用所选凭证替换全部目标SSH服务器的现有凭证。"
                : "使用所选凭证替换全部目标服务器的现有客户端凭证。"
              : bulkCredentialOperation === "add"
                ? "保留现有客户端凭证，并新增所选凭证。"
                : bulkCredentialKind === "server"
                  ? "清空全部目标SSH服务器当前关联的服务器凭证。"
                  : "保留其他客户端凭证，仅删除所选凭证。"}
          </Typography.Text>
        </Space>
      </Modal>
    </PageContainer>
  );
}

function TestStatus({ server }: { server: ServerRecord }) {
  if (!server.last_test_at || server.last_test_ok === null)
    return <span className="muted-text">尚未测试</span>;
  const time = new Date(server.last_test_at).toLocaleString();
  const status = (
    <span
      className={`test-result ${server.last_test_ok ? "success" : "failure"}`}
    >
      {server.last_test_ok ? "验证通过" : "连接失败"}
    </span>
  );
  return (
    <div className="test-status">
      {server.last_test_ok ? (
        status
      ) : (
        <Tooltip text={server.last_test_error || "未知错误"}>{status}</Tooltip>
      )}
      <span className="test-time">{time}</span>
    </div>
  );
}

const IMPORT_HEADER =
  "proxy_user,target_host,target_port,ownership,remark,server_credential_id,client_credential_id";
const LEGACY_IMPORT_HEADER =
  "proxy_user,target_host,target_port,server_credential_id,client_credential_id";

interface ParsedImportRow {
  line: number; // 1-based,含表头
  proxy_user: string;
  target_host: string;
  target_port: number;
  ownership: ServerOwnership;
  remark: string;
  server_credential_id: number | null;
  client_credential_ids: number[];
}

interface ImportRowResult {
  line: number;
  proxy_user: string;
  ok: boolean;
  message: string;
}

function parseImportCSV(
  text: string,
  serverCredentials: ServerCredential[],
  clientCredentials: ClientCredential[],
): { rows: ParsedImportRow[]; errors: string[] } {
  const errors: string[] = [];
  const lines = text
    .split(/\r\n|\n/)
    .filter((l, i, arr) => l.trim() !== "" || i < arr.length - 1);
  if (lines.length === 0) {
    return { rows: [], errors: ["内容不能为空"] };
  }

  const header = lines[0].trim();
  const legacyFormat = header === LEGACY_IMPORT_HEADER;
  if (header !== IMPORT_HEADER && !legacyFormat) {
    return { rows: [], errors: [`表头必须是: ${IMPORT_HEADER}`] };
  }

  const serverCredIds = new Set(serverCredentials.map((c) => c.id));
  const clientCredIds = new Set(clientCredentials.map((c) => c.id));

  const rows: ParsedImportRow[] = [];
  for (let i = 1; i < lines.length; i++) {
    const lineNo = i + 1;
    const raw = lines[i];
    if (raw.trim() === "") continue;
    const cols = raw.split(",");
    const expectedColumns = legacyFormat ? 5 : 7;
    if (cols.length !== expectedColumns) {
      errors.push(`第 ${lineNo} 行:应为 ${expectedColumns} 列,实际 ${cols.length} 列`);
      continue;
    }
    const values = cols.map((c) => c.trim());
    const [proxyUserRaw, targetHostRaw, targetPortRaw] = values;
    const ownershipRaw = legacyFormat ? "" : values[3];
    const remarkRaw = legacyFormat ? "" : values[4];
    const serverCredRaw = values[legacyFormat ? 3 : 5];
    const clientCredRaw = values[legacyFormat ? 4 : 6];

    if (!proxyUserRaw) {
      errors.push(`第 ${lineNo} 行:proxy_user不能为空`);
    }
    if (!targetHostRaw) {
      errors.push(`第 ${lineNo} 行:target_host不能为空`);
    }

    const validOwnerships: ServerOwnership[] = [
      "",
      "pve_vm",
      "physical",
      "cloud",
      "network_device",
    ];
    if (!validOwnerships.includes(ownershipRaw as ServerOwnership)) {
      errors.push(
        `第 ${lineNo} 行:ownership "${ownershipRaw}" 不合法,应为pve_vm、physical、cloud、network_device或留空`,
      );
    }

    let targetPort = 22;
    if (targetPortRaw !== "") {
      const n = Number(targetPortRaw);
      if (!Number.isInteger(n) || n < 1 || n > 65535) {
        errors.push(
          `第 ${lineNo} 行:target_port "${targetPortRaw}" 不合法,应为 1-65535 的整数或留空`,
        );
      } else {
        targetPort = n;
      }
    }

    let serverCredentialId: number | null = null;
    if (serverCredRaw !== "") {
      const n = Number(serverCredRaw);
      if (!Number.isInteger(n) || !serverCredIds.has(n)) {
        errors.push(
          `第 ${lineNo} 行:server_credential_id "${serverCredRaw}" 不存在`,
        );
      } else {
        serverCredentialId = n;
      }
    }

    const clientCredentialIds: number[] = [];
    if (clientCredRaw !== "") {
      for (const part of clientCredRaw.split(";")) {
        const p = part.trim();
        if (p === "") continue;
        const n = Number(p);
        if (!Number.isInteger(n) || !clientCredIds.has(n)) {
          errors.push(`第 ${lineNo} 行:client_credential_id "${p}" 不存在`);
        } else {
          clientCredentialIds.push(n);
        }
      }
    }

    rows.push({
      line: lineNo,
      proxy_user: proxyUserRaw,
      target_host: targetHostRaw,
      target_port: targetPort,
      ownership: ownershipRaw as ServerOwnership,
      remark: remarkRaw,
      server_credential_id: serverCredentialId,
      client_credential_ids: clientCredentialIds,
    });
  }

  return { rows, errors };
}

function ImportModal({
  servers,
  serverCredentials,
  clientCredentials,
  onClose,
  onDone,
}: {
  servers: ServerRecord[];
  serverCredentials: ServerCredential[];
  clientCredentials: ClientCredential[];
  onClose: () => void;
  onDone: () => Promise<void>;
}) {
  const [text, setText] = useState("");
  const [errors, setErrors] = useState<string[]>([]);
  const [results, setResults] = useState<ImportRowResult[] | null>(null);
  const [running, setRunning] = useState(false);

  async function runImport() {
    setResults(null);
    const { rows, errors: parseErrors } = parseImportCSV(
      text,
      serverCredentials,
      clientCredentials,
    );
    if (parseErrors.length > 0) {
      setErrors(parseErrors);
      return;
    }
    setErrors([]);
    setRunning(true);

    // 拷贝一份客户端凭证快照,让整批导入过程中的 proxy_users 变化在批内累积、
    // 又不直接改动父组件的 state(reconcileClientCredentials 会就地更新这份拷贝)。
    const workingCredentials = clientCredentials.map((c) => ({
      ...c,
      proxy_users: [...c.proxy_users],
    }));

    const existingLogins = new Set(servers.map((s) => s.proxy_user));
    const rowResults: ImportRowResult[] = [];
    for (const row of rows) {
      const wasExisting = existingLogins.has(row.proxy_user);
      try {
        await api.upsertServer({
          ...emptyServer,
          proxy_user: row.proxy_user,
          target_host: row.target_host,
          target_port: row.target_port,
          ownership: row.ownership,
          remark: row.remark,
          server_credential_id: row.server_credential_id,
        });
        await reconcileClientCredentials(
          workingCredentials,
          row.proxy_user,
          new Set(row.client_credential_ids),
        );
        rowResults.push({
          line: row.line,
          proxy_user: row.proxy_user,
          ok: true,
          message: wasExisting ? "已更新" : "已新增",
        });
      } catch (err) {
        rowResults.push({
          line: row.line,
          proxy_user: row.proxy_user,
          ok: false,
          message: err instanceof ApiError ? err.message : "失败",
        });
      }
    }

    setResults(rowResults);
    setRunning(false);
    await onDone();
  }

  return (
    <Modal
      open
      title="导入服务器（CSV）"
      onCancel={onClose}
      footer={null}
      width={760}
      styles={{
        body: { maxHeight: "70vh", overflowY: "auto", paddingTop: 16 },
      }}
    >
      <p className="mb-2 text-sm text-slate-500 ">
        第一行必须是表头,后面每行一台服务器。
        <code className="rounded bg-slate-100 px-1 ">proxy_user</code>{" "}
        是唯一键,已存在则覆盖更新,不存在则新增。
        <code className="rounded bg-slate-100 px-1 ">target_port</code>/
        <code className="rounded bg-slate-100 px-1 ">ownership</code>/
        <code className="rounded bg-slate-100 px-1 ">remark</code>/
        <code className="rounded bg-slate-100 px-1 ">server_credential_id</code>
        /
        <code className="rounded bg-slate-100 px-1 ">client_credential_id</code>{" "}
        可以留空。ownership支持pve_vm、physical、cloud、network_device；remark不能包含英文逗号；
        <code className="rounded bg-slate-100 px-1 ">
          client_credential_id
        </code>{" "}
        一个格子里可以用分号分隔多个id。
      </p>
      <pre className="mb-3 table-scroll overflow-x-auto rounded bg-slate-50 p-2 text-xs text-slate-600  ">
        {`${IMPORT_HEADER}
srv1,192.168.1.2,,pve_vm,测试环境,1,1;2
srv2,192.168.1.3,22,physical,机房物理机,,3
srv3,192.168.1.4,,cloud,云主机,,`}
      </pre>

      <Input.TextArea
        className="h-40 font-mono text-xs"
        placeholder={IMPORT_HEADER}
        value={text}
        onChange={(e) => setText(e.target.value)}
      />

      {errors.length > 0 && (
        <div className="mt-2 max-h-32 overflow-y-auto rounded border border-red-200 bg-red-50 p-2 text-xs text-red-700   ">
          {errors.map((e, i) => (
            <div key={i}>{e}</div>
          ))}
        </div>
      )}

      {results && (
        <div className="mt-2 max-h-48 overflow-y-auto rounded border border-slate-200 p-2 text-xs ">
          {results.map((r) => (
            <div
              key={r.line}
              className={r.ok ? "text-emerald-600 " : "text-red-600 "}
            >
              第 {r.line} 行 {r.proxy_user}:{r.message}
            </div>
          ))}
        </div>
      )}

      <div className="mt-4 flex justify-end gap-2">
        <Button onClick={onClose}>关闭</Button>
        <Button
          type="primary"
          onClick={runImport}
          disabled={running || text.trim() === ""}
        >
          {running ? "导入中..." : "导入"}
        </Button>
      </div>
    </Modal>
  );
}
