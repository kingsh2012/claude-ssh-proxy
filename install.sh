#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVICE_NAME="claude-ssh-proxy"
readonly INSTALL_DIR="${CLAUDE_SSH_PROXY_INSTALL_DIR:-/data/claude-ssh-proxy}"
readonly LEGACY_DATA_DIR="${CLAUDE_SSH_PROXY_LEGACY_DATA_DIR:-/var/lib/claude-ssh-proxy}"
readonly UNIT_PATH="${CLAUDE_SSH_PROXY_UNIT_PATH:-/etc/systemd/system/${SERVICE_NAME}.service}"
readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SOURCE_BINARY="${SCRIPT_DIR}/claude-ssh-proxy"
readonly SOURCE_UNIT="${SCRIPT_DIR}/systemd/${SERVICE_NAME}.service"
is_first_install=true
backup_dir=""
had_binary=false
had_unit=false
service_was_active=false
replacement_started=false

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

echo "正在安装 ${SERVICE_NAME} 到 ${INSTALL_DIR}..."
if systemctl is-active --quiet "${SERVICE_NAME}.service"; then
  service_was_active=true
  echo "正在停止现有服务..."
  systemctl stop "${SERVICE_NAME}.service"
fi

install -d -m 0700 -o root -g root "${INSTALL_DIR}"

if [[ ! -e ${INSTALL_DIR}/claude-ssh-proxy.db && -f ${LEGACY_DATA_DIR}/claude-ssh-proxy.db ]]; then
  echo "检测到旧数据目录,正在复制数据到 ${INSTALL_DIR}..."
  for name in claude-ssh-proxy.db claude-ssh-proxy.db-wal claude-ssh-proxy.db-shm claude-ssh-proxy.db.key host_key; do
    if [[ -f ${LEGACY_DATA_DIR}/${name} ]]; then
      install -m 0600 -o root -g root "${LEGACY_DATA_DIR}/${name}" "${INSTALL_DIR}/${name}"
    fi
  done
  echo "旧目录 ${LEGACY_DATA_DIR} 已保留,确认升级正常后可自行归档。"
fi

if [[ -f ${INSTALL_DIR}/claude-ssh-proxy.db ]]; then
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
  for name in claude-ssh-proxy.db claude-ssh-proxy.db-wal claude-ssh-proxy.db-shm claude-ssh-proxy.db.key host_key; do
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

replacement_started=true
install -m 0755 -o root -g root "${SOURCE_BINARY}" "${INSTALL_DIR}/${SERVICE_NAME}.new"
mv -f "${INSTALL_DIR}/${SERVICE_NAME}.new" "${INSTALL_DIR}/${SERVICE_NAME}"
install -m 0644 -o root -g root "${SOURCE_UNIT}" "${UNIT_PATH}"

systemctl daemon-reload
systemctl enable "${SERVICE_NAME}.service" >/dev/null

if ! systemctl start "${SERVICE_NAME}.service"; then
  echo "错误:服务启动失败,最近日志如下:" >&2
  journalctl -u "${SERVICE_NAME}.service" -n 30 --no-pager >&2 || true
  false
fi

rm -f "${INSTALL_DIR}/${SERVICE_NAME}.previous" "${UNIT_PATH}.previous"
replacement_started=false

echo
echo "安装完成。"
echo "  程序版本: $("${INSTALL_DIR}/${SERVICE_NAME}" -version)"
echo "  数据目录: ${INSTALL_DIR}"
if [[ -n ${backup_dir} ]]; then
  echo "  本次备份: ${backup_dir}"
fi
echo "  Web 后台: http://127.0.0.1:8080"
echo "  服务状态: systemctl status ${SERVICE_NAME}"
echo "  查看日志: journalctl -u ${SERVICE_NAME} -f"
echo
if [[ ${is_first_install} == true ]]; then
  echo "首次安装的管理员账号为 admin/admin,登录后必须修改密码。"
fi
