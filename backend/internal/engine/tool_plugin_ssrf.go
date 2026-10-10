package engine

// 工具插件端点的 SSRF 红线（runtime-agility M3）。
//
// 热加载把任意声明式配置变成一等 HTTP 工具：endpoint 若不设防，一个
// 指向 169.254.169.254（云元数据）/127.0.0.1/内网服务的插件就是打进
// agent 循环的 SSRF 跳板。防线与包安装器（docs/34 §3）同水位：
//
//  1. 注册时（validatePluginEndpoint）：仅 http/https；主机名解析后拒绝
//     环回/私网/链路本地/保留/组播地址；任一解析结果落在保留段即整体拒绝。
//  2. 执行时（pluginHTTPClient）：重定向逐跳重新校验，拒绝跨入保留段。
//
// 已知残留：解析与连接之间存在 DNS rebinding 窗口（与安装器同款残留），
// v1 不做连接级 pinning，靠注册时校验 + 重定向防线收敛风险面。

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// validatePluginEndpoint 校验插件工具的 endpoint：协议白名单 + 解析后的
// 全部 IP 必须公开可路由。注册时调用，拒绝即不注册。
func validatePluginEndpoint(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("plugin tool endpoint is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("plugin tool endpoint unparseable: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("plugin tool endpoint scheme %q not allowed (http/https only)", parsed.Scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("plugin tool endpoint has no host: %q", rawURL)
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	infos, err := net.DefaultResolver.LookupIPAddr(context.Background(), host)
	if err != nil {
		return fmt.Errorf("plugin tool endpoint host %q unresolvable: %w", host, err)
	}
	if len(infos) == 0 {
		return fmt.Errorf("plugin tool endpoint host %q resolved to no addresses", host)
	}
	for _, info := range infos {
		ip := info.IP
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || inCGNAT(ip) {
			return fmt.Errorf("plugin tool endpoint %q resolves to private/reserved address %s — blocked", rawURL, ip)
		}
	}
	_ = port // 端口仅用于文档语义；保留段判定与端口无关
	return nil
}

// inCGNAT 报告地址是否落在 100.64/10（运营商级 NAT 段——IsPrivate 不覆盖，
// 但同样不可公网路由）。
func inCGNAT(ip net.IP) bool {
	cgnat := net.IPv4(100, 64, 0, 0)
	mask := net.CIDRMask(10, 32)
	return ip.To4() != nil && ip.To4().Mask(mask).Equal(cgnat.Mask(mask))
}

// pluginHTTPClient 返回带重定向逐跳校验的执行客户端。
func pluginHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			if err := validatePluginEndpoint(req.URL.String()); err != nil {
				return fmt.Errorf("redirect blocked: %w", err)
			}
			return nil
		},
	}
}
