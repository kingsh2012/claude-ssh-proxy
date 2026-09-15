package main

import (
	"flag"
	"fmt"
	"github.com/kingsh2012/aiagent-ssh-proxy/internal/agenttls"
	"log"
	"net"
	"strings"
)

func main() {
	caDir := flag.String("ca-dir", "", "独立保管CA证书和私钥的目录，已有CA会复用")
	out := flag.String("out", "", "服务器证书输出目录，拒绝覆盖已有文件")
	ipValues := flag.String("ip", "", "客户端实际连接的公网IP，多个用逗号分隔")
	dnsValues := flag.String("dns", "", "可选域名，多个用逗号分隔")
	flag.Parse()
	var ips []net.IP
	if *ipValues != "" {
		for _, value := range strings.Split(*ipValues, ",") {
			ip := net.ParseIP(strings.TrimSpace(value))
			if ip == nil {
				log.Fatal("无效IP地址")
			}
			ips = append(ips, ip)
		}
	}
	var names []string
	if *dnsValues != "" {
		for _, value := range strings.Split(*dnsValues, ",") {
			value = strings.TrimSpace(value)
			if value == "" || strings.ContainsAny(value, " /:@") {
				log.Fatal("无效域名")
			}
			names = append(names, value)
		}
	}
	if err := agenttls.Generate(*caDir, *out, ips, names); err != nil {
		log.Fatal(err)
	}
	fmt.Println("证书已生成。服务器仅需server.crt和server.key；客户端构建仅需ca.crt。CA私钥请独立保管。")
}
