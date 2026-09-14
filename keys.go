package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"

	"golang.org/x/crypto/ssh"
)

// loadOrCreateHostKey 加载 proxy 自身对外展示的 host key;不存在则自动生成一个 ed25519 密钥。
func loadOrCreateHostKey(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return ssh.ParsePrivateKey(data)
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取 host key 失败: %w", err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成 host key 失败: %w", err)
	}

	block, err := ssh.MarshalPrivateKey(priv, "aiagent-ssh-proxy host key")
	if err != nil {
		return nil, fmt.Errorf("序列化 host key 失败: %w", err)
	}
	pemBytes := pem.EncodeToMemory(block)
	if err := os.WriteFile(path, pemBytes, 0600); err != nil {
		return nil, fmt.Errorf("写入 host key 失败: %w", err)
	}

	return ssh.ParsePrivateKey(pemBytes)
}

// parsePrivateKey 解析用于连接后端真实服务器的私钥内容(PEM 文本,存在 DB 里,不是文件路径)。
func parsePrivateKey(pemContent, passphrase string) (ssh.Signer, error) {
	if passphrase != "" {
		return ssh.ParsePrivateKeyWithPassphrase([]byte(pemContent), []byte(passphrase))
	}
	return ssh.ParsePrivateKey([]byte(pemContent))
}
