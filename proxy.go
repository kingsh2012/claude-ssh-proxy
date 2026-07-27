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
		// 有些目标机器(比如 ESXi 内置的 SSH 服务、部分网络设备如 H3C 交换机)不支持标准的
		// "password" 认证方式,只支持 "keyboard-interactive"。OpenSSH 客户端默认的认证方式
		// 优先级是 keyboard-interactive 排在 password 前面,这里保持一致的顺序:先试
		// keyboard-interactive,不行再退回 password。部分设备一次连接只允许尝试一种方式,
		// 顺序反了会导致 password 尝试失败后直接断线,连不上本来能连的设备。
		authMethods = append(authMethods,
			ssh.KeyboardInteractive(func(name, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = server.AuthPassword
				}
				return answers, nil
			}),
			ssh.Password(server.AuthPassword),
		)
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

	// 部分老旧交换机/网络设备只支持过时的弱算法(CBC 类 cipher、老 KEX、ssh-dss host key),
	// x/crypto/ssh 默认不启用这些不安全算法。只有服务器显式勾选了"兼容旧设备"才在默认算法
	// 后面追加这些兜底选项,避免所有服务器都被动接受弱算法、扩大安全面。
	if server.LegacyAlgorithms {
		supported := ssh.SupportedAlgorithms()
		insecure := ssh.InsecureAlgorithms()
		clientCfg.Config.Ciphers = append(append([]string{}, supported.Ciphers...), insecure.Ciphers...)
		clientCfg.Config.KeyExchanges = append(append([]string{}, supported.KeyExchanges...), insecure.KeyExchanges...)
		clientCfg.HostKeyAlgorithms = append(append([]string{}, supported.HostKeys...), insecure.HostKeys...)
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
		var reader io.Reader = upChan
		if audit != nil {
			reader = io.TeeReader(upChan, outputWriter{audit}) // 捕获 server->client 方向的数据(exec 命令的输出)
		}
		io.Copy(downChan, reader)
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
