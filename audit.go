package main

import (
	"encoding/binary"
	"log"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

const auditMaxDetailBytes = 64 * 1024

// auditSession 收集一个 "session" channel(exec/shell/subsystem)在生命周期内的
// 关键信息:执行的命令、shell 阶段的输入、命令的返回结果、退出码,分两步落库:
// 一收到 exec/shell/subsystem 请求就立刻插入一条 status="running" 的记录,
// channel 结束时再回写 output/exit_status 并把 status 改成 "completed"。
// 这样即使命令执行到一半代理进程崩了,或者客户端 Ctrl+C/断线导致连接异常中断,
// 数据库里也能留下"这条命令启动过、但没有正常结束"的痕迹,而不是完全查不到。
//
// eventType/command/exitStatus/dbID 会被 downReqs/upReqs 两个 forwardRequests
// goroutine 并发读写,detail/output 会被两个 io.Copy goroutine 并发读写,
// 所以都用 mu 保护。
type auditSession struct {
	store                 *Store
	proxyUser             string
	remoteAddr            string
	targetHost            string
	targetPort            int
	clientCredentialLabel string

	mu         sync.Mutex
	dbID       int64 // 0 表示还没插入过
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
// 或 server->client 的 exit-status),提取审计需要的信息。第一次确定 eventType 时立刻插入一条
// "运行中"的记录,后续同一个 channel 上的请求不会重复插入。
func (a *auditSession) noteRequest(req *ssh.Request) {
	a.mu.Lock()
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
	needInsert := a.dbID == 0 && a.eventType != ""
	var startRecord AuditLog
	if needInsert {
		startRecord = AuditLog{
			ProxyUser:             a.proxyUser,
			RemoteAddr:            a.remoteAddr,
			TargetHost:            a.targetHost,
			TargetPort:            a.targetPort,
			EventType:             a.eventType,
			Command:               a.command,
			Status:                "running",
			ClientCredentialLabel: a.clientCredentialLabel,
		}
	}
	a.mu.Unlock()

	if needInsert {
		id, err := a.store.InsertAuditLog(startRecord)
		if err != nil {
			log.Printf("写入审计日志失败: %v", err)
			return
		}
		a.mu.Lock()
		a.dbID = id
		a.mu.Unlock()
	}
}

// Write 让 auditSession 可以作为 io.Writer 接到 TeeReader 上,捕获 shell 阶段客户端敲的内容。
func (a *auditSession) Write(p []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
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
	w.mu.Lock()
	defer w.mu.Unlock()
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

// finish 在 channel 结束时(不管是正常执行完、客户端 Ctrl+C、还是网络断开)调用,
// 把最终的 output/exit_status 回写到 noteRequest 阶段插入的那一行,状态改成
// "completed"。如果这条记录从没插入过(比如纯端口转发,没有 exec/shell/subsystem
// 请求),什么都不做。exit_status 仍然是 nil 说明 channel 结束前没收到目标机器的
// 退出码——多半就是连接异常中断(Ctrl+C 之类),而不是命令正常跑完。
func (a *auditSession) finish() {
	a.mu.Lock()
	dbID := a.dbID
	output := a.output.String()
	detail := a.detail.String()
	exitStatus := a.exitStatus
	truncated := a.detailTr || a.outputTr
	a.mu.Unlock()

	if dbID == 0 {
		return
	}
	if err := a.store.CompleteAuditLog(dbID, output, detail, exitStatus, truncated); err != nil {
		log.Printf("回写审计日志失败: %v", err)
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
