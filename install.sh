#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVICE_NAME="ops-ssh-proxy"
readonly INSTALL_DIR="${OPS_SSH_PROXY_INSTALL_DIR:-/data/ops-ssh-proxy}"
readonly LEGACY_DATA_DIR="${OPS_SSH_PROXY_LEGACY_DATA_DIR:-/var/lib/ops-ssh-proxy}"
readonly UNIT_PATH="${OPS_SSH_PROXY_UNIT_PATH:-/etc/systemd/system/${SERVICE_NAME}.service}"
readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SOURCE_BINARY="${SCRIPT_DIR}/ops-ssh-proxy"
readonly SOURCE_UNIT="${SCRIPT_DIR}/systemd/${SERVICE_NAME}.service"
readonly CONFIG_PATH="${INSTALL_DIR}/${SERVICE_NAME}.env"
is_first_install=true
backup_dir=""
ssh_listen_addr=""
web_listen_addr=""
ssh_addr_provided=false
had_binary=false
had_unit=false
had_config=false
service_was_active=false
replacement_started=false

usage() {
  cat <<'EOF'
用法: ./install.sh [选项]

  --ssh-addr 地址   覆盖并保存 SSH 代理监听地址,例如 :2223
  --web-addr 地址   Web 后台监听地址,例如 127.0.0.1:8080
  -h, --help        显示帮助

不传 --ssh-addr 时,保留数据库里的 SSH 监听配置。
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --ssh-addr|--web-addr)
      if [[ $# -lt 2 || -z $2 ]]; then
        echo "错误:$1 需要提供监听地址。" >&2
        exit 2
      fi
      if [[ $1 == --ssh-addr ]]; then
        ssh_listen_addr=$2
        ssh_addr_provided=true
      else
        web_listen_addr=$2
      fi
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "错误:未知选项 $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

rollback_install() {
  local status=$?
  if [[ ${status} -eq 0 ]]; then
    return
  fi

  trap - ERR
  echo "错误:安装失败。" >&2
  if [[ ${replacement_started} == true ]]; then
    echo "正在尝试恢复上一版本..." >&2
    if [[ ${had_binary} == true && -f ${INSTALL_DIR}/${SERVICE_NAME}.previous ]]; then
      mv -f "${INSTALL_DIR}/${SERVICE_NAME}.previous" "${INSTALL_DIR}/${SERVICE_NAME}"
    else
      rm -f "${INSTALL_DIR}/${SERVICE_NAME}" "${INSTALL_DIR}/${SERVICE_NAME}.previous"
    fi
    if [[ ${had_unit} == true && -f ${UNIT_PATH}.previous ]]; then
      mv -f "${UNIT_PATH}.previous" "${UNIT_PATH}"
    else
      systemctl disable "${SERVICE_NAME}.service" >/dev/null 2>&1 || true
      rm -f "${UNIT_PATH}" "${UNIT_PATH}.previous"
    fi
    if [[ ${had_config} == true && -f ${CONFIG_PATH}.previous ]]; then
      mv -f "${CONFIG_PATH}.previous" "${CONFIG_PATH}"
    elif [[ ${had_config} == false ]]; then
      rm -f "${CONFIG_PATH}" "${CONFIG_PATH}.previous"
    fi
    systemctl daemon-reload >/dev/null 2>&1 || true
  fi
  if [[ ${service_was_active} == true ]]; then
    systemctl start "${SERVICE_NAME}.service" >/dev/null 2>&1 || true
  fi
  exit "${status}"
}

trap rollback_install ERR

if [[ ${EUID} -ne 0 ]]; then
  echo "错误:请使用 root 运行此脚本。" >&2
  exit 1
fi

for command in install systemctl; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "错误:缺少命令 ${command}。" >&2
    exit 1
  fi
done

if [[ ! -x ${SOURCE_BINARY} ]]; then
  echo "错误:安装包中找不到 ${SOURCE_BINARY}。" >&2
  exit 1
fi
if [[ ! -f ${SOURCE_UNIT} ]]; then
  echo "错误:安装包中找不到 ${SOURCE_UNIT}。" >&2
  exit 1
fi

if ! "${SOURCE_BINARY}" -version >/dev/null; then
  echo "错误:二进制无法在当前系统运行,请确认下载的是正确架构的安装包。" >&2
  exit 1
fi

# Renamed installations require an explicit migration to preserve the real unit
# arguments, database key and listeners (custom installations vary).
if systemctl cat claude-ssh-proxy.service >/dev/null 2>&1 || [[ -f /data/claude-ssh-proxy/claude-ssh-proxy.db || -f /var/lib/claude-ssh-proxy/claude-ssh-proxy.db ]]; then
  echo "检测到 claude-ssh-proxy 旧部署。请按 Windows Agent 文档中的更名迁移说明保留数据和监听参数后迁移，不可作为全新安装覆盖。" >&2
  exit 1
fi

echo "正在安装 ${SERVICE_NAME} 到 ${INSTALL_DIR}..."
if systemctl is-active --quiet "${SERVICE_NAME}.service"; then
  service_was_active=true
  echo "正在停止现有服务..."
  systemctl stop "${SERVICE_NAME}.service"
fi

install -d -m 0700 -o root -g root "${INSTALL_DIR}"

if [[ ! -e ${INSTALL_DIR}/ops-ssh-proxy.db && -f ${LEGACY_DATA_DIR}/ops-ssh-proxy.db ]]; then
  echo "检测到旧数据目录,正在复制数据到 ${INSTALL_DIR}..."
  for name in ops-ssh-proxy.db ops-ssh-proxy.db-wal ops-ssh-proxy.db-shm ops-ssh-proxy.db.key host_key; do
    if [[ -f ${LEGACY_DATA_DIR}/${name} ]]; then
      install -m 0600 -o root -g root "${LEGACY_DATA_DIR}/${name}" "${INSTALL_DIR}/${name}"
    fi
  done
  echo "旧目录 ${LEGACY_DATA_DIR} 已保留,确认升级正常后可自行归档。"
fi

if [[ -f ${INSTALL_DIR}/ops-ssh-proxy.db ]]; then
  is_first_install=false
  backup_root="${INSTALL_DIR}/backups"
  backup_base="${backup_root}/$(date -u +%Y%m%dT%H%M%SZ)"
  backup_dir="${backup_base}"
  backup_suffix=1
  while [[ -e ${backup_dir} ]]; do
    backup_dir="${backup_base}-${backup_suffix}"
    ((backup_suffix += 1))
  done
  echo "正在备份现有数据到 ${backup_dir}..."
  install -d -m 0700 -o root -g root "${backup_dir}"
  for name in ops-ssh-proxy.db ops-ssh-proxy.db-wal ops-ssh-proxy.db-shm ops-ssh-proxy.db.key host_key; do
    if [[ -f ${INSTALL_DIR}/${name} ]]; then
      install -m 0600 -o root -g root "${INSTALL_DIR}/${name}" "${backup_dir}/${name}"
    fi
  done
fi

if [[ -f ${INSTALL_DIR}/${SERVICE_NAME} ]]; then
  cp -a "${INSTALL_DIR}/${SERVICE_NAME}" "${INSTALL_DIR}/${SERVICE_NAME}.previous"
  had_binary=true
fi
if [[ -f ${UNIT_PATH} ]]; then
  cp -a "${UNIT_PATH}" "${UNIT_PATH}.previous"
  had_unit=true
fi
if [[ -f ${CONFIG_PATH} ]]; then
  cp -a "${CONFIG_PATH}" "${CONFIG_PATH}.previous"
  had_config=true
  while IFS='=' read -r key value; do
    case "${key}" in
      SSH_LISTEN_ADDR) stored_ssh_listen_addr=${value} ;;
      WEB_LISTEN_ADDR) stored_web_listen_addr=${value} ;;
    esac
  done < "${CONFIG_PATH}"
fi

if [[ ${ssh_addr_provided} == false ]]; then
  ssh_listen_addr=""
fi
web_listen_addr=${web_listen_addr:-${stored_web_listen_addr:-127.0.0.1:8080}}
for listen_addr in "${web_listen_addr}"; do
  if [[ -z ${listen_addr} || ${listen_addr} =~ [^A-Za-z0-9._:%\[\]-] ]]; then
    echo "错误:监听地址 ${listen_addr} 格式不正确。" >&2
    false
  fi
done
if [[ -n ${ssh_listen_addr} && ${ssh_listen_addr} =~ [^A-Za-z0-9._:%\[\]-] ]]; then
  echo "错误:监听地址 ${ssh_listen_addr} 格式不正确。" >&2
  false
fi

replacement_started=true
install -m 0755 -o root -g root "${SOURCE_BINARY}" "${INSTALL_DIR}/${SERVICE_NAME}.new"
mv -f "${INSTALL_DIR}/${SERVICE_NAME}.new" "${INSTALL_DIR}/${SERVICE_NAME}"
install -m 0644 -o root -g root "${SOURCE_UNIT}" "${UNIT_PATH}"
{
  printf 'SSH_LISTEN_ADDR=%s\n' "${ssh_listen_addr}"
  printf 'WEB_LISTEN_ADDR=%s\n' "${web_listen_addr}"
} > "${CONFIG_PATH}.new"
chmod 0600 "${CONFIG_PATH}.new"
chown root:root "${CONFIG_PATH}.new"
mv -f "${CONFIG_PATH}.new" "${CONFIG_PATH}"

systemctl daemon-reload
systemctl enable "${SERVICE_NAME}.service" >/dev/null

if ! systemctl start "${SERVICE_NAME}.service"; then
  echo "错误:服务启动失败,最近日志如下:" >&2
  journalctl -u "${SERVICE_NAME}.service" -n 30 --no-pager >&2 || true
  false
fi
sleep 1
if ! systemctl is-active --quiet "${SERVICE_NAME}.service"; then
  echo "错误:服务启动后退出,可能是监听端口被占用。最近日志如下:" >&2
  journalctl -u "${SERVICE_NAME}.service" -n 30 --no-pager >&2 || true
  false
fi

# SSH 覆盖值已由程序写入数据库,清空环境变量以免后续重启覆盖网页中的新配置。
{
  printf 'SSH_LISTEN_ADDR=\n'
  printf 'WEB_LISTEN_ADDR=%s\n' "${web_listen_addr}"
} > "${CONFIG_PATH}.new"
chmod 0600 "${CONFIG_PATH}.new"
chown root:root "${CONFIG_PATH}.new"
mv -f "${CONFIG_PATH}.new" "${CONFIG_PATH}"

rm -f "${INSTALL_DIR}/${SERVICE_NAME}.previous" "${UNIT_PATH}.previous" "${CONFIG_PATH}.previous"
replacement_started=false

echo
echo "安装完成。"
echo "  程序版本: $("${INSTALL_DIR}/${SERVICE_NAME}" -version)"
echo "  数据目录: ${INSTALL_DIR}"
if [[ -n ${backup_dir} ]]; then
  echo "  本次备份: ${backup_dir}"
fi
if [[ ${ssh_addr_provided} == true ]]; then
  echo "  SSH 监听: ${ssh_listen_addr}"
else
  echo "  SSH 监听: 保留数据库配置(首次安装默认 :2222)"
fi
echo "  Web 监听: ${web_listen_addr}"
echo "  服务状态: systemctl status ${SERVICE_NAME}"
echo "  查看日志: journalctl -u ${SERVICE_NAME} -f"
echo
if [[ ${is_first_install} == true ]]; then
  echo "首次安装的管理员账号为 admin/admin,登录后必须修改密码。"
fi
