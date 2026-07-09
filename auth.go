package main

import (
	"bytes"
	"fmt"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"
)

// buildPublicKeyCallback 每次认证尝试都查库:根据登录用户名找到服务器(必须存在且启用),
// 再看关联到这台服务器的客户端凭据(client_credentials,多对多关系)里有没有公钥类型匹配的。
func buildPublicKeyCallback(store *Store) func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	return func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		user := conn.User()
		server, err := store.GetServer(user)
		if err != nil || !server.Enabled {
			return nil, fmt.Errorf("用户 %q 不可用", user)
		}
		creds, err := store.ListClientCredentialsForServer(user)
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

// buildPasswordCallback 是公钥认证之外的备用登录方式:关联到这台服务器的客户端凭据里,
// 密码类型的任意一份匹配即可登录(用户名仍然决定转发到哪台目标机器)。
func buildPasswordCallback(store *Store) func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
	return func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		user := conn.User()
		server, err := store.GetServer(user)
		if err != nil || !server.Enabled {
			return nil, fmt.Errorf("用户 %q 不可用", user)
		}
		creds, err := store.ListClientCredentialsForServer(user)
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
