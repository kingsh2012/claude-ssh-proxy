package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

type Proxy struct {
	agents     *AgentHub
	store      *Store
	hostSigner ssh.Signer

	mu         sync.Mutex
	listener   net.Listener
	listenAddr string

	connectionsMu sync.RWMutex
	connections   map[uint64]*ActiveConnection
	nextConnID    uint64
}

type ActiveConnection struct {
	ID                    uint64    `json:"id"`
	ProxyUser             string    `json:"proxy_user"`
	RemoteAddr            string    `json:"remote_addr"`
	TargetHost            string    `json:"target_host"`
	TargetPort            int       `json:"target_port"`
	TargetUser            string    `json:"target_user"`
	ClientCredentialLabel string    `json:"client_credential_label"`
	ConnectedAt           time.Time `json:"connected_at"`
	ActiveSessions        int       `json:"active_sessions"`
}

func NewProxy(store *Store, hostKeyPath string) (*Proxy, error) {
	signer, err := loadOrCreateHostKey(hostKeyPath)
	if err != nil {
		return nil, err
	}
	return &Proxy{agents: NewAgentHub(store), store: store, hostSigner: signer, connections: make(map[uint64]*ActiveConnection)}, nil
}

// Start 在指定地址上监听并开始接受连接(非阻塞,内部起 goroutine 处理 accept 循环)。
func (p *Proxy) Start(addr string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listener != nil {
		return fmt.Errorf("SSH proxy 已经在监听 %s", p.listenAddr)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", addr, err)
	}
	p.activateListener(ln, addr)
	return nil
}

func (p *Proxy) activateListener(ln net.Listener, addr string) {
	serverCfg := &ssh.ServerConfig{
		PublicKeyCallback: buildPublicKeyCallback(p.store),
		PasswordCallback:  buildPasswordCallback(p.store),
	}
	serverCfg.AddHostKey(p.hostSigner)

	p.listener = ln
	p.listenAddr = addr

	log.Printf("aiagent-ssh-proxy 正在监听 %s", addr)

	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				log.Printf("accept 失败: %v", err)
				return
			}
			go p.handleConn(nc, serverCfg)
		}
	}()
}

// Stop 关闭当前监听,供切换监听地址时调用。
func (p *Proxy) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listener != nil {
		p.listener.Close()
		p.listener = nil
		p.listenAddr = ""
	}
}

func (p *Proxy) ListenAddr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.listenAddr
}

// Restart 优先先绑定新地址,成功后才关闭旧监听。监听地址与旧地址端口重叠时无法同时
// 绑定,此时才短暂关闭旧监听再重试;重试失败会自动恢复旧监听。
func (p *Proxy) Restart(addr string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.listener == nil {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("监听 %s 失败: %w", addr, err)
		}
		p.activateListener(ln, addr)
		return nil
	}
	if addr == p.listenAddr {
		return nil
	}

	oldListener := p.listener
	oldAddr := p.listenAddr
	newListener, err := net.Listen("tcp", addr)
	if err == nil {
		p.activateListener(newListener, addr)
		_ = oldListener.Close()
		return nil
	}

	// 只有新旧地址使用同一个 TCP 端口且失败原因是地址占用时,失败才可能是旧
	// listener 自身造成的。其他错误直接返回,保持旧监听不动。
	if !errors.Is(err, syscall.EADDRINUSE) || !sameTCPPort(oldListener.Addr(), addr) {
		return fmt.Errorf("监听 %s 失败: %w", addr, err)
	}

	_ = oldListener.Close()
	newListener, retryErr := net.Listen("tcp", addr)
	if retryErr == nil {
		p.activateListener(newListener, addr)
		return nil
	}

	restored, restoreErr := net.Listen("tcp", oldAddr)
	if restoreErr == nil {
		p.activateListener(restored, oldAddr)
		return fmt.Errorf("监听 %s 失败,已恢复旧监听 %s: %w", addr, oldAddr, retryErr)
	}

	p.listener = nil
	p.listenAddr = ""
	return fmt.Errorf("监听 %s 失败且无法恢复旧监听 %s: %v (恢复失败: %v)", addr, oldAddr, retryErr, restoreErr)
}

func sameTCPPort(current net.Addr, requested string) bool {
	currentTCP, ok := current.(*net.TCPAddr)
	if !ok || currentTCP.Port == 0 {
		return false
	}
	requestedTCP, err := net.ResolveTCPAddr("tcp", requested)
	return err == nil && requestedTCP.Port == currentTCP.Port
}

func (p *Proxy) addConnection(server ServerRecord, remoteAddr, credentialLabel string) uint64 {
	p.connectionsMu.Lock()
	defer p.connectionsMu.Unlock()
	p.nextConnID++
	id := p.nextConnID
	p.connections[id] = &ActiveConnection{
		ID: id, ProxyUser: server.ProxyUser, RemoteAddr: remoteAddr,
		TargetHost: server.TargetHost, TargetPort: server.TargetPort, TargetUser: server.TargetUser,
		ClientCredentialLabel: credentialLabel, ConnectedAt: time.Now(),
	}
	return id
}

func (p *Proxy) removeConnection(id uint64) {
	p.connectionsMu.Lock()
	delete(p.connections, id)
	p.connectionsMu.Unlock()
}

func (p *Proxy) changeActiveSessions(id uint64, delta int) {
	p.connectionsMu.Lock()
	if c := p.connections[id]; c != nil {
		c.ActiveSessions += delta
	}
	p.connectionsMu.Unlock()
}

func (p *Proxy) ActiveConnections() []ActiveConnection {
	p.connectionsMu.RLock()
	out := make([]ActiveConnection, 0, len(p.connections))
	for _, c := range p.connections {
		out = append(out, *c)
	}
	p.connectionsMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ConnectedAt.Before(out[j].ConnectedAt) })
	return out
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
	server, err := p.store.ResolveServer(proxyUser)
	if err != nil || !server.Enabled {
		log.Printf("[%s] 服务器 %q 不存在", remoteAddr, proxyUser)
		return
	}

	log.Printf("[%s] 用户 %q 认证通过,路由到 %s@%s:%d",
		remoteAddr, proxyUser, server.TargetUser, server.TargetHost, server.TargetPort)

	if server.ConnectionType == "agent" {
		p.handleAgentSSH(sconn, chans, reqs, *server, remoteAddr, clientCredentialLabel)
		return
	}

	client, err := dialUpstream(*server)
	if err != nil {
		log.Printf("[%s] 连接后端 %s:%d 失败: %v", remoteAddr, server.TargetHost, server.TargetPort, err)
		return
	}
	defer client.Close()
	connectionID := p.addConnection(*server, remoteAddr, clientCredentialLabel)
	defer p.removeConnection(connectionID)

	go ssh.DiscardRequests(reqs) // 全局请求(如 keepalive)直接丢弃,不影响会话代理

	var wg sync.WaitGroup
	for newChan := range chans {
		wg.Add(1)
		go func(nch ssh.NewChannel) {
			defer wg.Done()
			p.forwardChannel(nch, client, proxyUser, remoteAddr, server.TargetHost, server.TargetPort, clientCredentialLabel, connectionID)
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
		HostKeyCallback: verifyHostKeyFingerprint(server.HostKeyFingerprint),
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

func verifyHostKeyFingerprint(expected string) ssh.HostKeyCallback {
	if expected == "" {
		return ssh.InsecureIgnoreHostKey()
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		actual := ssh.FingerprintSHA256(key)
		if actual != expected {
			return fmt.Errorf("目标机器 host key 指纹不匹配:期望 %s,实际 %s", expected, actual)
		}
		return nil
	}
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
func (p *Proxy) forwardChannel(newChan ssh.NewChannel, client *ssh.Client, proxyUser, remoteAddr, targetHost string, targetPort int, clientCredentialLabel string, connectionID uint64) {
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
		p.changeActiveSessions(connectionID, 1)
		defer p.changeActiveSessions(connectionID, -1)
		audit = newAuditSession(p.store, proxyUser, remoteAddr, targetHost, targetPort, clientCredentialLabel)
		defer audit.finish()
	}

	var requestWG sync.WaitGroup
	requestWG.Add(2)
	go func() {
		defer requestWG.Done()
		forwardRequests(downReqs, upChan, audit)
	}()
	go func() {
		defer requestWG.Done()
		forwardRequests(upReqs, downChan, audit)
	}()

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
	_ = upChan.Close()
	_ = downChan.Close()
	requestWG.Wait()
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
