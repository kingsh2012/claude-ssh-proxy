package main

import (
	"encoding/binary"
	"log"
	"strings"

	"golang.org/x/crypto/ssh"
)

const auditMaxDetailBytes = 64 * 1024

// auditSession 收集一个 "session" channel(exec/shell/subsystem)在生命周期内的
// 关键信息:执行的命令、shell 阶段的输入输出、退出码,结束时落一条审计记录。
type auditSession struct {
	store                 *Store
	routeUser             string
	remoteAddr            string
	targetHost            string
	targetPort            int
	clientCredentialLabel string

	eventType  string
	detail     strings.Builder
	truncated  bool
	exitStatus *int
}

func newAuditSession(store *Store, routeUser, remoteAddr, targetHost string, targetPort int, clientCredentialLabel string) *auditSession {
	return &auditSession{
		store: store, routeUser: routeUser, remoteAddr: remoteAddr,
		targetHost: targetHost, targetPort: targetPort, clientCredentialLabel: clientCredentialLabel,
	}
}

// noteRequest 观察 session channel 上的 out-of-band 请求(client->server 的 exec/shell/subsystem,
// 或 server->client 的 exit-status),提取审计需要的信息。
func (a *auditSession) noteRequest(req *ssh.Request) {
	switch req.Type {
	case "exec":
		if cmd, ok := parseSSHString(req.Payload); ok {
			a.eventType = "exec"
			a.appendDetail("$ " + cmd + "\n")
		}
	case "shell":
		if a.eventType == "" {
			a.eventType = "shell"
		}
	case "subsystem":
		if name, ok := parseSSHString(req.Payload); ok {
			a.eventType = "subsystem:" + name
		}
	case "exit-status":
		if len(req.Payload) >= 4 {
			v := int(binary.BigEndian.Uint32(req.Payload))
			a.exitStatus = &v
		}
	}
}

// Write 让 auditSession 可以作为 io.Writer 接到 TeeReader 上,捕获 shell 阶段客户端敲的内容。
func (a *auditSession) Write(p []byte) (int, error) {
	a.appendDetail(string(p))
	return len(p), nil
}

func (a *auditSession) appendDetail(s string) {
	if a.truncated {
		return
	}
	remain := auditMaxDetailBytes - a.detail.Len()
	if remain <= 0 {
		a.truncated = true
		return
	}
	if len(s) > remain {
		s = s[:remain]
		a.truncated = true
	}
	a.detail.WriteString(s)
}

func (a *auditSession) finish() {
	if a.eventType == "" {
		return // 没有 exec/shell/subsystem 请求(比如纯端口转发),不记录
	}
	err := a.store.InsertAuditLog(AuditLog{
		RouteUser:             a.routeUser,
		RemoteAddr:            a.remoteAddr,
		TargetHost:            a.targetHost,
		TargetPort:            a.targetPort,
		EventType:             a.eventType,
		Detail:                a.detail.String(),
		ExitStatus:            a.exitStatus,
		Truncated:             a.truncated,
		ClientCredentialLabel: a.clientCredentialLabel,
	})
	if err != nil {
		log.Printf("写入审计日志失败: %v", err)
	}
}

// parseSSHString 解析 SSH 请求 payload 里的第一个字符串字段(4 字节长度前缀 + 内容),
// exec/subsystem 请求的 payload 格式就是单个这样的字符串。
func parseSSHString(payload []byte) (string, bool) {
	if len(payload) < 4 {
		return "", false
	}
	n := binary.BigEndian.Uint32(payload[:4])
	if uint64(4+n) > uint64(len(payload)) {
		return "", false
	}
	return string(payload[4 : 4+n]), true
}
