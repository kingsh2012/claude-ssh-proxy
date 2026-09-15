package main

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"flag"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

func main() {
	ca := flag.String("ca", "", "要嵌入客户端的CA公钥证书PEM路径，不接受私钥")
	out := flag.String("out", "dist/aiagent-ssh-client.exe", "Windows amd64客户端输出路径")
	version := flag.String("version", "dev", "写入客户端的版本号")
	flag.Parse()
	if *ca == "" {
		log.Fatal("必须指定-ca公钥证书路径")
	}
	data, err := os.ReadFile(*ca)
	if err != nil {
		log.Fatal("无法读取CA证书")
	}
	if len(data) > 64<<10 {
		log.Fatal("CA证书不能超过64KiB")
	}
	rest, count := data, 0
	for len(strings.TrimSpace(string(rest))) > 0 {
		block, next := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" {
			log.Fatal("CA文件只能包含公钥证书，不能包含私钥")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA {
			log.Fatal("需要有效CA证书")
		}
		count++
		rest = next
	}
	if count == 0 {
		log.Fatal("CA证书为空")
	}
	if !regexp.MustCompile(`^(dev|v[0-9]+\.[0-9]+\.[0-9]+)$`).MatchString(*version) {
		log.Fatal("版本号必须是dev或vX.Y.Z")
	}
	flags := "-s -w -X main.version=" + *version + " -X github.com/kingsh2012/aiagent-ssh-proxy/internal/winagent.embeddedCABase64=" + base64.StdEncoding.EncodeToString(data)
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", flags, "-o", *out, "./cmd/windows-agent")
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "GOOS") && !strings.EqualFold(key, "GOARCH") && !strings.EqualFold(key, "CGO_ENABLED") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatal("客户端构建失败")
	}
	log.Println("已构建内置CA的Windows客户端，未嵌入CA私钥或注册Token")
}
