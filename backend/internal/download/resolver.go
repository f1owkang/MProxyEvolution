// Package download 提供适配 Android 的 HTTP 下载能力。
//
// Android 上没有 /etc/resolv.conf，Go 默认 resolver 无法工作；本包读取
// getprop net.dns1..net.dns4，使用 golang.org/x/net/dns/dnsmessage 自行发送
// UDP A/AAAA 查询。Windows/桌面平台回退到系统 resolver。
package download

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"mproxy/internal/sysutil"
)

const (
	defaultDNSTimeout = 5 * time.Second
	defaultCacheTTL   = 5 * time.Minute
)

type cacheEntry struct {
	ips     []net.IP
	expires time.Time
}

// Resolver 是 Android 友好的 DNS 解析器。
type Resolver struct {
	mu        sync.Mutex
	cache     map[string]cacheEntry
	servers   []string
	useSystem bool
	timeout   time.Duration
}

// NewResolver 根据平台创建解析器：Android 读取 getprop，其他平台使用系统 resolver。
func NewResolver() *Resolver {
	r := &Resolver{cache: map[string]cacheEntry{}, timeout: defaultDNSTimeout}
	if !sysutil.IsAndroid() {
		r.useSystem = true
		return r
	}
	r.servers = androidDNSServers()
	if len(r.servers) == 0 {
		// getprop 失败时使用公共 DNS 兜底
		r.servers = []string{"223.5.5.5:53", "119.29.29.29:53", "8.8.8.8:53"}
	}
	return r
}

// NewResolverWithServers 创建使用固定 DNS 服务器的解析器（测试用）。
func NewResolverWithServers(servers ...string) *Resolver {
	normalized := make([]string, 0, len(servers))
	for _, s := range servers {
		normalized = append(normalized, normalizeServer(s))
	}
	return &Resolver{cache: map[string]cacheEntry{}, servers: normalized, timeout: 2 * time.Second}
}

// Servers 返回当前使用的 DNS 服务器列表。
func (r *Resolver) Servers() []string {
	if r.useSystem {
		return []string{"system"}
	}
	return append([]string(nil), r.servers...)
}

func androidDNSServers() []string {
	var out []string
	seen := map[string]bool{}
	for i := 1; i <= 4; i++ {
		ip := strings.TrimSpace(sysutil.GetProp(fmt.Sprintf("net.dns%d", i)))
		if ip == "" || seen[ip] {
			continue
		}
		seen[ip] = true
		out = append(out, normalizeServer(ip))
	}
	return out
}

func normalizeServer(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if _, _, err := net.SplitHostPort(s); err == nil {
		return s
	}
	if strings.Contains(s, ":") && net.ParseIP(s) == nil {
		// 形如 2001:db8::1 的 IPv6
		return "[" + s + "]:53"
	}
	return net.JoinHostPort(s, "53")
}

// Lookup 解析主机名为 IP 列表（带缓存）。
func (r *Resolver) Lookup(ctx context.Context, host string) ([]net.IP, error) {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" {
		return nil, fmt.Errorf("主机名为空")
	}
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	if r.useSystem {
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("解析域名 %s 失败: %w", host, err)
		}
		ips := make([]net.IP, 0, len(addrs))
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("域名 %s 没有可用 IP", host)
		}
		return ips, nil
	}

	key := strings.ToLower(host)
	r.mu.Lock()
	if e, ok := r.cache[key]; ok && time.Now().Before(e.expires) {
		ips := append([]net.IP(nil), e.ips...)
		r.mu.Unlock()
		return ips, nil
	}
	r.mu.Unlock()

	servers := append([]string(nil), r.servers...)
	if len(servers) == 0 {
		return nil, fmt.Errorf("没有可用的 DNS 服务器")
	}
	var lastErr error
	for _, server := range servers {
		ips, ttl, err := r.queryBoth(ctx, host, server)
		if err != nil {
			lastErr = err
			continue
		}
		if len(ips) > 0 {
			if ttl <= 0 {
				ttl = defaultCacheTTL
			}
			r.mu.Lock()
			r.cache[key] = cacheEntry{ips: append([]net.IP(nil), ips...), expires: time.Now().Add(ttl)}
			r.mu.Unlock()
			return ips, nil
		}
		lastErr = fmt.Errorf("DNS 服务器 %s 未返回 %s 的地址", server, host)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("解析域名 %s 失败", host)
	}
	return nil, lastErr
}

// queryBoth 同时查询 A 与 AAAA，返回合并后的地址与最小 TTL。
func (r *Resolver) queryBoth(ctx context.Context, host, server string) ([]net.IP, time.Duration, error) {
	type result struct {
		ips []net.IP
		ttl time.Duration
		err error
	}
	ch := make(chan result, 2)
	types := []dnsmessage.Type{dnsmessage.TypeA, dnsmessage.TypeAAAA}
	for _, qt := range types {
		go func(qt dnsmessage.Type) {
			ips, ttl, err := r.query(ctx, host, server, qt)
			ch <- result{ips: ips, ttl: ttl, err: err}
		}(qt)
	}
	var all []net.IP
	minTTL := defaultCacheTTL
	var firstErr error
	for i := 0; i < 2; i++ {
		res := <-ch
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
			continue
		}
		all = append(all, res.ips...)
		if res.ttl > 0 && res.ttl < minTTL {
			minTTL = res.ttl
		}
	}
	if len(all) == 0 && firstErr != nil {
		return nil, 0, firstErr
	}
	return dedupeIPs(all), minTTL, nil
}

func dedupeIPs(in []net.IP) []net.IP {
	seen := map[string]bool{}
	out := make([]net.IP, 0, len(in))
	for _, ip := range in {
		key := ip.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ip)
	}
	return out
}

func (r *Resolver) query(ctx context.Context, host, server string, qtype dnsmessage.Type) ([]net.IP, time.Duration, error) {
	query, id, err := buildQuery(host, qtype)
	if err != nil {
		return nil, 0, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "udp", server)
	if err != nil {
		return nil, 0, fmt.Errorf("连接 DNS 服务器 %s 失败: %w", server, err)
	}
	defer conn.Close()

	timeout := r.timeout
	if dl, ok := ctx.Deadline(); ok {
		if remain := time.Until(dl); remain < timeout {
			timeout = remain
		}
	}
	if timeout <= 0 {
		timeout = defaultDNSTimeout
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(query); err != nil {
		return nil, 0, fmt.Errorf("发送 DNS 查询失败: %w", err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, 0, fmt.Errorf("读取 DNS 响应失败: %w", err)
	}
	ips, ttl, truncated, err := parseResponse(buf[:n], id, host)
	if err != nil {
		return nil, 0, err
	}
	if truncated {
		// UDP 响应被截断时改用 TCP 重试
		tcpIPs, tcpTTL, tcpErr := r.queryTCP(ctx, host, server, qtype)
		if tcpErr == nil {
			return tcpIPs, tcpTTL, nil
		}
	}
	return ips, ttl, nil
}

func (r *Resolver) queryTCP(ctx context.Context, host, server string, qtype dnsmessage.Type) ([]net.IP, time.Duration, error) {
	query, id, err := buildQuery(host, qtype)
	if err != nil {
		return nil, 0, err
	}
	d := net.Dialer{Timeout: r.timeout}
	conn, err := d.DialContext(ctx, "tcp", server)
	if err != nil {
		return nil, 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(r.timeout))
	packet := make([]byte, 2+len(query))
	binary.BigEndian.PutUint16(packet, uint16(len(query)))
	copy(packet[2:], query)
	if _, err := conn.Write(packet); err != nil {
		return nil, 0, err
	}
	var lenBuf [2]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return nil, 0, err
	}
	resp := make([]byte, binary.BigEndian.Uint16(lenBuf[:]))
	if _, err := io.ReadFull(conn, resp); err != nil {
		return nil, 0, err
	}
	ips, ttl, _, err := parseResponse(resp, id, host)
	return ips, ttl, err
}

// buildQuery 构造 DNS 查询报文。
func buildQuery(host string, qtype dnsmessage.Type) ([]byte, uint16, error) {
	var idBuf [2]byte
	if _, err := rand.Read(idBuf[:]); err != nil {
		return nil, 0, err
	}
	id := binary.BigEndian.Uint16(idBuf[:])
	name, err := dnsmessage.NewName(withTrailingDot(host))
	if err != nil {
		return nil, 0, fmt.Errorf("构造 DNS 查询失败: %w", err)
	}
	msg := dnsmessage.Message{
		Header: dnsmessage.Header{
			ID:               id,
			RecursionDesired: true,
		},
		Questions: []dnsmessage.Question{{
			Name:  name,
			Type:  qtype,
			Class: dnsmessage.ClassINET,
		}},
	}
	packed, err := msg.Pack()
	if err != nil {
		return nil, 0, fmt.Errorf("打包 DNS 查询失败: %w", err)
	}
	return packed, id, nil
}

// parseResponse 解析 DNS 响应报文。
func parseResponse(packet []byte, id uint16, host string) ([]net.IP, time.Duration, bool, error) {
	var msg dnsmessage.Message
	if err := msg.Unpack(packet); err != nil {
		return nil, 0, false, fmt.Errorf("解析 DNS 响应失败: %w", err)
	}
	if msg.Header.ID != id {
		return nil, 0, false, fmt.Errorf("DNS 响应 ID 不匹配")
	}
	if msg.Header.RCode != dnsmessage.RCodeSuccess {
		return nil, 0, msg.Header.Truncated, fmt.Errorf("DNS 查询失败: %s", msg.Header.RCode)
	}
	var ips []net.IP
	minTTL := time.Duration(defaultCacheTTL)
	for _, ans := range msg.Answers {
		ttl := time.Duration(ans.Header.TTL) * time.Second
		if ttl > 0 && ttl < minTTL {
			minTTL = ttl
		}
		switch body := ans.Body.(type) {
		case *dnsmessage.AResource:
			ips = append(ips, net.IP(append([]byte(nil), body.A[:]...)))
		case *dnsmessage.AAAAResource:
			ips = append(ips, net.IP(append([]byte(nil), body.AAAA[:]...)))
		}
	}
	return ips, minTTL, msg.Header.Truncated, nil
}

func withTrailingDot(host string) string {
	if strings.HasSuffix(host, ".") {
		return host
	}
	return host + "."
}

// DialContext 实现自定义拨号：先自解析域名再连接，供 HTTP Transport 使用。
func (r *Resolver) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	if ip := net.ParseIP(host); ip != nil {
		return dialer.DialContext(ctx, network, addr)
	}
	ips, err := r.Lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ip := range ips {
		conn, derr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if derr == nil {
			return conn, nil
		}
		lastErr = derr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("无法连接到 %s", addr)
	}
	return nil, lastErr
}

// ResolveAddr 解析 host:port 中的 host。
func (r *Resolver) ResolveAddr(ctx context.Context, addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || net.ParseIP(host) != nil {
		return addr, err
	}
	ips, err := r.Lookup(ctx, host)
	if err != nil || len(ips) == 0 {
		return addr, err
	}
	return net.JoinHostPort(ips[0].String(), port), nil
}
