import {
  ModalForm,
  ProFormText,
  ProFormTextArea,
  ProFormSelect,
  ProFormDependency,
} from "@ant-design/pro-components";
import type { ProFormInstance } from "@ant-design/pro-components";
import { useRef } from "react";
import type { ServerRecord, ServerCredential, ClientCredential } from "./api";

type ServerValues = Omit<ServerCredential, "id">;
type ClientValues = Omit<ClientCredential, "id" | "has_password">;
const required = (label: string) => [
  { required: true, message: `请输入${label}` },
];
const hostOptions = (servers: ServerRecord[]) =>
  servers.map((s) => ({ label: s.proxy_user, value: s.proxy_user }));

export function ServerCredentialForm({
  initial,
  servers,
  onSave,
  onClose,
}: {
  initial: ServerValues & { id?: number };
  servers: ServerRecord[];
  onSave: (v: ServerValues) => Promise<boolean>;
  onClose: () => void;
}) {
  const edit = initial.id != null;
  return (
    <ModalForm<ServerValues>
      key={initial.id ?? "new"}
      open
      title={edit ? "编辑服务器凭证" : "新建服务器凭证"}
      width={520}
      autoComplete="off"
      initialValues={initial}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      modalProps={{
        destroyOnHidden: true,
        styles: { body: { maxHeight: "70vh", overflowY: "auto" } },
      }}
      submitter={{ searchConfig: { submitText: "保存" } }}
      onFinish={onSave}
    >
      <ProFormText
        name="label"
        label="名称"
        placeholder="请输入凭证名称"
        rules={required("名称")}
      />
      <ProFormText
        name="target_user"
        label="SSH登录名"
        rules={required("SSH登录名")}
        fieldProps={{ autoComplete: "new-password" }}
      />
      <ProFormSelect
        name="auth_type"
        label="认证方式"
        allowClear={false}
        options={[
          { label: "密码", value: "password" },
          { label: "私钥", value: "private_key" },
        ]}
      />
      <ProFormDependency name={["auth_type"]}>
        {({ auth_type }) =>
          auth_type === "password" ? (
            <ProFormText.Password
              name="auth_password"
              label="密码"
              placeholder={edit ? "留空则不修改" : "请输入密码"}
              rules={edit ? [] : required("密码")}
              fieldProps={{ autoComplete: "new-password" }}
            />
          ) : (
            <>
              <ProFormTextArea
                name="auth_private_key"
                label="私钥内容（PEM）"
                placeholder={edit ? "留空则不修改" : "请输入私钥"}
                rules={edit ? [] : required("私钥")}
                fieldProps={{ rows: 5, autoComplete: "off" }}
              />
              <ProFormText.Password
                name="auth_private_key_passphrase"
                label="私钥口令"
                placeholder={edit ? "留空则不修改" : "可选"}
                fieldProps={{ autoComplete: "new-password" }}
              />
            </>
          )
        }
      </ProFormDependency>
      <ProFormSelect
        name="proxy_users"
        label="绑定的服务器"
        mode="multiple"
        options={hostOptions(
          servers.filter((s) => s.connection_type !== "agent"),
        )}
        placeholder="选择服务器"
        fieldProps={{ optionFilterProp: "label" }}
      />
    </ModalForm>
  );
}
export function ClientCredentialForm({
  initial,
  servers,
  onSave,
  onClose,
}: {
  initial: ClientValues & { id?: number; has_password?: boolean };
  servers: ServerRecord[];
  onSave: (v: ClientValues) => Promise<boolean>;
  onClose: () => void;
}) {
  const formRef = useRef<ProFormInstance<ClientValues>>(undefined);
  const autoLabel = useRef(
    !initial.label ||
      initial.label ===
        (initial.public_key?.trim().split(/\s+/).slice(2).join(" ") || ""),
  );
  const edit = initial.id != null;
  return (
    <ModalForm<ClientValues>
      key={initial.id ?? "new"}
      open
      title={edit ? "编辑客户端凭证" : "新建客户端凭证"}
      width={520}
      autoComplete="off"
      initialValues={initial}
      formRef={formRef}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      modalProps={{
        destroyOnHidden: true,
        styles: { body: { maxHeight: "70vh", overflowY: "auto" } },
      }}
      submitter={{ searchConfig: { submitText: "保存" } }}
      onFinish={onSave}
      onValuesChange={(changed) => {
        if ("label" in changed) autoLabel.current = false;
        if ("public_key" in changed && autoLabel.current) {
          const parts = (changed.public_key || "").trim().split(/\s+/);
          formRef.current?.setFieldsValue({
            label: parts.length >= 3 ? parts[parts.length - 1] : "",
          });
        }
      }}
    >
      <ProFormText
        name="label"
        label="名称"
        placeholder="请输入凭证名称"
        rules={required("名称")}
      />
      <ProFormSelect
        name="auth_type"
        label="认证方式"
        allowClear={false}
        options={[
          { label: "公钥", value: "public_key" },
          { label: "密码", value: "password" },
        ]}
      />
      <ProFormDependency name={["auth_type"]}>
        {({ auth_type }) =>
          auth_type === "public_key" ? (
            <ProFormTextArea
              name="public_key"
              label="公钥内容"
              placeholder="ssh-ed25519 AAAA... client"
              rules={required("公钥内容")}
              fieldProps={{ rows: 5, autoComplete: "off" }}
            />
          ) : (
            <ProFormText.Password
              name="password"
              label="密码"
              placeholder={
                edit && initial.has_password ? "留空则不修改" : "请输入密码"
              }
              rules={edit && initial.has_password ? [] : required("密码")}
              fieldProps={{ autoComplete: "new-password" }}
            />
          )
        }
      </ProFormDependency>
      <ProFormSelect
        name="proxy_users"
        label="绑定的服务器"
        mode="multiple"
        options={hostOptions(servers)}
        placeholder="选择服务器"
        fieldProps={{ optionFilterProp: "label" }}
      />
    </ModalForm>
  );
}
