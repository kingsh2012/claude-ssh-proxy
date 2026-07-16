package main

import (
	"encoding/binary"
	"log"
	"strings"

	"golang.org/x/crypto/ssh"
)

const auditMaxDetailBytes = 64 * 1024

// auditSession 收集一个 "session" channel(exec/shell/subsystem)在生命周期内的
// 关键信息:执行的命令、shell 阶段的输入、命令的返回结果、退出码,结束时落一条审计记录。
type auditSession struct {
	store                 *Store
	proxyUser             string
	remoteAddr            string
	targetHost            string
	targetPort            int
	clientCredentialLabel string

	eventType  string
	command    string // 只在 exec 场景下有值:命令原文
	output     strings.Builder
	outputTr   bool
	detail     strings.Builder // shell/subsystem 场景下客户端敲的原始内容
	detailTr   bool
	exitStatus *int
}

func newAuditSession(store *Store, proxyUser, remoteAddr, targetHost string, targetPort int, clientCredentialLabel string) *auditSession {
	return &auditSession{
		store: store, proxyUser: proxyUser, remoteAddr: remoteAddr,
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
			a.command = cmd
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
	if !a.detailTr {
		s := string(p)
		remain := auditMaxDetailBytes - a.detail.Len()
		if remain <= 0 {
			a.detailTr = true
		} else {
			if len(s) > remain {
				s = s[:remain]
				a.detailTr = true
			}
			a.detail.WriteString(s)
		}
	}
	return len(p), nil
}

// outputWriter 用来捕获 server->client 方向的数据,也就是命令的返回结果。
// 只有 exec 场景(一次性命令,比如 `ssh host "ls -la"`)才记录:这种输出通常是干净的
// stdout/stderr 文本。交互式 shell 会话如果也记录这个方向,拿到的是原始终端字节流,
// 里面全是光标控制符和 ANSI 转义序列,存下来是乱码,所以特意跳过。
type outputWriter struct {
	*auditSession
}

func (w outputWriter) Write(p []byte) (int, error) {
	if w.eventType != "exec" || w.outputTr {
		return len(p), nil
	}
	s := string(p)
	remain := auditMaxDetailBytes - w.output.Len()
	if remain <= 0 {
		w.outputTr = true
		return len(p), nil
	}
	if len(s) > remain {
		s = s[:remain]
		w.outputTr = true
	}
	w.output.WriteString(s)
	return len(p), nil
}

func (a *auditSession) finish() {
	if a.eventType == "" {
		return // 没有 exec/shell/subsystem 请求(比如纯端口转发),不记录
	}
	err := a.store.InsertAuditLog(AuditLog{
		ProxyUser:             a.proxyUser,
		RemoteAddr:            a.remoteAddr,
		TargetHost:            a.targetHost,
		TargetPort:            a.targetPort,
		EventType:             a.eventType,
		Command:               a.command,
		Output:                a.output.String(),
		Detail:                a.detail.String(),
		ExitStatus:            a.exitStatus,
		Truncated:             a.detailTr || a.outputTr,
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
