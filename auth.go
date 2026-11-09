package main

import (
	"bytes"
	"fmt"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"
)

// buildPublicKeyCallback 每次认证尝试都查库:根据登录用户名找到服务器(必须存在且启用),
// 再看关联到这台服务器的客户端凭证(client_credentials,多对多关系)里有没有公钥类型匹配的。
func buildPublicKeyCallback(store *Store) func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	return func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		user := conn.User()
		server, err := store.ResolveServer(user)
		if err != nil || !server.Enabled {
			return nil, fmt.Errorf("用户 %q不可用", user)
		}
		creds, err := store.ListClientCredentialsForServerID(server.ID)
		if err != nil {
			return nil, fmt.Errorf("未知用户名 %q", user)
		}
		for _, c := range creds {
			if c.AuthType != "public_key" || c.PublicKey == "" {
				continue
			}
			allowed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(c.PublicKey))
			if err != nil {
				continue
			}
			if bytes.Equal(allowed.Marshal(), key.Marshal()) {
				return &ssh.Permissions{
					Extensions: map[string]string{"server-user": user, "client-credential-label": c.Label},
				}, nil
			}
		}
		return nil, fmt.Errorf("公钥不匹配用户 %q", user)
	}
}

// buildPasswordCallback 是公钥认证之外的备用登录方式:关联到这台服务器的客户端凭证里,
// 密码类型的任意一份匹配即可登录(用户名仍然决定转发到哪台目标机器)。
func buildPasswordCallback(store *Store) func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
	return func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		user := conn.User()
		server, err := store.ResolveServer(user)
		if err != nil || !server.Enabled {
			return nil, fmt.Errorf("用户 %q不可用", user)
		}
		creds, err := store.ListClientCredentialsForServerID(server.ID)
		if err != nil {
			return nil, fmt.Errorf("未知用户名 %q", user)
		}
		for _, c := range creds {
			if c.AuthType != "password" || !c.HasPassword {
				continue
			}
			if bcrypt.CompareHashAndPassword([]byte(c.passwordHash), password) == nil {
				return &ssh.Permissions{
					Extensions: map[string]string{"server-user": user, "client-credential-label": c.Label},
				}, nil
			}
		}
		return nil, fmt.Errorf("密码不匹配用户 %q", user)
	}
}

// newServerConfig creates authentication state for one connection. The initial
// "none" request ONLY selects methods; it never grants anonymous access.
func (p *Proxy) newServerConfig() *ssh.ServerConfig {
	publicKey := buildPublicKeyCallback(p.store)
	password := buildPasswordCallback(p.store)
	var preAuth ssh.ServerPreAuthConn
	warned := false
	config := &ssh.ServerConfig{
		NoClientAuth:        true,
		PreAuthConnCallback: func(conn ssh.ServerPreAuthConn) { preAuth = conn },
	}
	config.PublicKeyCallback = func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		permissions, err := publicKey(conn, key)
		if err != nil && !warned {
			warned = true
			return nil, &ssh.BannerError{Err: err, Message: "SSH Proxy：本次提供的公钥未获此主机授权，或目标不可用。\r\n请检查私钥及主机授权；如有其他密钥，客户端将继续尝试。\r\n"}
		}
		return permissions, err
	}
	config.PasswordCallback = func(conn ssh.ConnMetadata, value []byte) (*ssh.Permissions, error) {
		permissions, err := password(conn, value)
		if err != nil {
			return nil, &ssh.BannerError{Err: err, Message: "SSH Proxy：客户端密码错误、未获此主机授权，或目标不可用。\r\n此处需要客户端凭据密码，不是目标服务器密码。\r\n"}
		}
		return permissions, nil
	}
	config.NoClientAuthCallback = func(conn ssh.ConnMetadata) (*ssh.Permissions, error) {
		next := ssh.ServerAuthCallbacks{PublicKeyCallback: config.PublicKeyCallback}
		server, err := p.store.ResolveServer(conn.User())
		if err != nil || !server.Enabled {
			if preAuth != nil {
				_ = preAuth.SendAuthBanner("SSH Proxy：目标不存在、已禁用或暂不可用，请联系管理员核对。\r\n")
			}
			return nil, &ssh.PartialSuccessError{Next: next}
		}
		credentials, err := p.store.ListClientCredentialsForServerID(server.ID)
		if err != nil {
			return nil, &ssh.PartialSuccessError{Next: next}
		}
		if len(credentials) == 0 && preAuth != nil {
			_ = preAuth.SendAuthBanner("SSH Proxy：此主机尚未授权任何客户端凭据，请联系管理员授权。\r\n")
		}
		for _, credential := range credentials {
			if credential.AuthType == "password" && credential.HasPassword {
				next.PasswordCallback = config.PasswordCallback
				break
			}
		}
		// This is an SSH method-selection step, not successful authentication.
		// A signed authorized key or matching client password is still mandatory.
		return nil, &ssh.PartialSuccessError{Next: next}
	}
	config.AddHostKey(p.hostSigner)
	return config
}
