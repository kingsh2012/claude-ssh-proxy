package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type Proxy struct {
	store      *Store
	hostSigner ssh.Signer

	mu       sync.Mutex
	listener net.Listener
	stopped  bool
}

func NewProxy(store *Store, hostKeyPath string) (*Proxy, error) {
	signer, err := loadOrCreateHostKey(hostKeyPath)
	if err != nil {
		return nil, err
	}
	return &Proxy{store: store, hostSigner: signer}, nil
}

// Start 在指定地址上监听并开始接受连接(非阻塞,内部起 goroutine 处理 accept 循环)。
func (p *Proxy) Start(addr string) error {
	serverCfg := &ssh.ServerConfig{
		PublicKeyCallback: buildPublicKeyCallback(p.store),
		PasswordCallback:  buildPasswordCallback(p.store),
	}
	serverCfg.AddHostKey(p.hostSigner)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", addr, err)
	}

	p.mu.Lock()
	p.listener = ln
	p.stopped = false
	p.mu.Unlock()

	log.Printf("claude-ssh-proxy 正在监听 %s", addr)

	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				p.mu.Lock()
				stopped := p.stopped
				p.mu.Unlock()
				if stopped {
					return
				}
				log.Printf("accept 失败: %v", err)
				return
			}
			go p.handleConn(nc, serverCfg)
		}
	}()

	return nil
}

// Stop 关闭当前监听,供切换监听地址时调用。
func (p *Proxy) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listener != nil {
		p.stopped = true
		p.listener.Close()
		p.listener = nil
	}
}

// Restart 停掉旧监听,换到新地址重新监听。
func (p *Proxy) Restart(addr string) error {
	p.Stop()
	return p.Start(addr)
}

func (p *Proxy) handleConn(nc net.Conn, serverCfg *ssh.ServerConfig) {
	defer nc.Close()
	remoteAddr := nc.RemoteAddr().String()

	sconn, chans, reqs, err := ssh.NewServerConn(nc, serverCfg)
	if err != nil {
		log.Printf("[%s] 握手/认证失败: %v", remoteAddr, err)
		return
	}
	defer sconn.Close()

	proxyUser := sconn.Permissions.Extensions["server-user"]
	clientCredentialLabel := sconn.Permissions.Extensions["client-credential-label"]
	server, err := p.store.GetServer(proxyUser)
	if err != nil {
		log.Printf("[%s] 服务器 %q 不存在", remoteAddr, proxyUser)
		return
	}

	log.Printf("[%s] 用户 %q 认证通过,路由到 %s@%s:%d",
		remoteAddr, proxyUser, server.TargetUser, server.TargetHost, server.TargetPort)

	client, err := dialUpstream(*server)
	if err != nil {
		log.Printf("[%s] 连接后端 %s:%d 失败: %v", remoteAddr, server.TargetHost, server.TargetPort, err)
		return
	}
	defer client.Close()

	go ssh.DiscardRequests(reqs) // 全局请求(如 keepalive)直接丢弃,不影响会话代理

	var wg sync.WaitGroup
	for newChan := range chans {
		wg.Add(1)
		go func(nch ssh.NewChannel) {
			defer wg.Done()
			p.forwardChannel(nch, client, proxyUser, remoteAddr, server.TargetHost, server.TargetPort, clientCredentialLabel)
		}(newChan)
	}
	wg.Wait()
}

func dialUpstream(server ServerRecord) (*ssh.Client, error) {
	return dialUpstreamTimeout(server, 15*time.Second)
}

// testUpstreamTimeout 用于"测试 SSH 连接"功能:比正常业务连接给一个更短的超时,
// 避免某台机器不可达时,测试请求(尤其是"测试全部")卡太久。
const testUpstreamTimeout = 8 * time.Second

func dialUpstreamTimeout(server ServerRecord, timeout time.Duration) (*ssh.Client, error) {
	var authMethods []ssh.AuthMethod
	switch server.AuthType {
	case "password":
		authMethods = append(authMethods, ssh.Password(server.AuthPassword))
	case "private_key":
		signer, err := parsePrivateKey(server.AuthPrivateKey, server.AuthPrivateKeyPassphrase)
		if err != nil {
			return nil, fmt.Errorf("解析私钥失败: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	default:
		return nil, fmt.Errorf("未知认证方式 %q", server.AuthType)
	}

	clientCfg := &ssh.ClientConfig{
		User:            server.TargetUser,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // 内网环境使用;需要更严格校验时换成 ssh.FixedHostKey
		Timeout:         timeout,
	}

	addr := fmt.Sprintf("%s:%d", server.TargetHost, server.TargetPort)
	return ssh.Dial("tcp", addr, clientCfg)
}

// TestServer 尝试连接一次目标机器验证账号密码/私钥是否配置正确,连上就立刻断开,
// 不做任何业务操作,供 Web 后台的"测试 SSH 连接"功能使用。
func TestServer(server ServerRecord) error {
	client, err := dialUpstreamTimeout(server, testUpstreamTimeout)
	if err != nil {
		return err
	}
	return client.Close()
}

// forwardChannel 把下游(Claude 侧)发起的一个 channel 对应地在上游(真实目标机器)
// 打开一个同类型 channel,双向转发数据和 out-of-band 请求;对 "session" 类型的
// channel(exec/shell/subsystem)顺带记录审计日志。
func (p *Proxy) forwardChannel(newChan ssh.NewChannel, client *ssh.Client, proxyUser, remoteAddr, targetHost string, targetPort int, clientCredentialLabel string) {
	upChan, upReqs, err := client.OpenChannel(newChan.ChannelType(), newChan.ExtraData())
	if err != nil {
		if openErr, ok := err.(*ssh.OpenChannelError); ok {
			newChan.Reject(openErr.Reason, openErr.Message)
		} else {
			newChan.Reject(ssh.ConnectionFailed, err.Error())
		}
		return
	}
	defer upChan.Close()

	downChan, downReqs, err := newChan.Accept()
	if err != nil {
		return
	}
	defer downChan.Close()

	var audit *auditSession
	if newChan.ChannelType() == "session" {
		audit = newAuditSession(p.store, proxyUser, remoteAddr, targetHost, targetPort, clientCredentialLabel)
		defer audit.finish()
	}

	go forwardRequests(downReqs, upChan, audit)
	go forwardRequests(upReqs, downChan, audit)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		var reader io.Reader = downChan
		if audit != nil {
			reader = io.TeeReader(downChan, audit) // 捕获 client->server 方向的数据(shell 里敲的命令)
		}
		io.Copy(upChan, reader)
		upChan.CloseWrite()
	}()
	go func() {
		defer wg.Done()
		io.Copy(downChan, upChan)
		downChan.CloseWrite()
	}()
	wg.Wait()
}

// forwardRequests 把一侧收到的 out-of-band 请求(pty-req/shell/exec/env/window-change/exit-status 等)
// 原样转发给另一侧,并把 reply 结果传回去;顺带喂给 audit 做审计记录。
func forwardRequests(in <-chan *ssh.Request, out ssh.Channel, audit *auditSession) {
	for req := range in {
		if audit != nil {
			audit.noteRequest(req)
		}
		ok, err := out.SendRequest(req.Type, req.WantReply, req.Payload)
		if req.WantReply {
			if err != nil {
				req.Reply(false, nil)
			} else {
				req.Reply(ok, nil)
			}
		}
	}
}
