package download

import (
	"bytes"
	"compress/gzip"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// startTestDNSServer 启动一个最小 UDP DNS 服务器，A 查询返回 1.2.3.4，AAAA 返回 ::1。
func startTestDNSServer(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听 UDP 失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			var query dnsmessage.Message
			if err := query.Unpack(buf[:n]); err != nil || len(query.Questions) == 0 {
				continue
			}
			q := query.Questions[0]
			resp := dnsmessage.Message{
				Header: dnsmessage.Header{
					ID:                 query.Header.ID,
					Response:           true,
					RecursionDesired:   true,
					RecursionAvailable: true,
					RCode:              dnsmessage.RCodeSuccess,
				},
				Questions: query.Questions,
			}
			switch q.Type {
			case dnsmessage.TypeA:
				resp.Answers = []dnsmessage.Resource{{
					Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
					Body:   &dnsmessage.AResource{A: [4]byte{1, 2, 3, 4}},
				}}
			case dnsmessage.TypeAAAA:
				resp.Answers = []dnsmessage.Resource{{
					Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeAAAA, Class: dnsmessage.ClassINET, TTL: 60},
					Body:   &dnsmessage.AAAAResource{AAAA: [16]byte{15: 1}},
				}}
			}
			packed, err := resp.Pack()
			if err != nil {
				continue
			}
			_, _ = conn.WriteTo(packed, addr)
		}
	}()
	return conn.LocalAddr().String()
}

func TestResolverCustomDNS(t *testing.T) {
	server := startTestDNSServer(t)
	r := NewResolverWithServers(server)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := r.Lookup(ctx, "example.com")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(ips) != 2 {
		t.Fatalf("期望 2 个地址（A+AAAA），得到 %v", ips)
	}
	if ips[0].String() != "1.2.3.4" && ips[1].String() != "1.2.3.4" {
		t.Fatalf("未返回 A 记录: %v", ips)
	}
	// 第二次应命中缓存
	ips2, err := r.Lookup(ctx, "example.com")
	if err != nil || len(ips2) != 2 {
		t.Fatalf("缓存解析失败: %v %v", ips2, err)
	}
}

func TestResolverIPLiteral(t *testing.T) {
	r := NewResolverWithServers("127.0.0.1:53")
	ips, err := r.Lookup(context.Background(), "192.0.2.1")
	if err != nil || len(ips) != 1 || ips[0].String() != "192.0.2.1" {
		t.Fatalf("IP 字面量应直接返回: %v %v", ips, err)
	}
}

func TestBuildQueryAndParseResponse(t *testing.T) {
	packed, id, err := buildQuery("example.com", dnsmessage.TypeA)
	if err != nil {
		t.Fatalf("构造查询失败: %v", err)
	}
	var msg dnsmessage.Message
	if err := msg.Unpack(packed); err != nil {
		t.Fatalf("查询报文不可解析: %v", err)
	}
	if msg.Header.ID != id || len(msg.Questions) != 1 || msg.Questions[0].Type != dnsmessage.TypeA {
		t.Fatalf("查询报文内容错误: %+v", msg)
	}
	if !msg.Header.RecursionDesired {
		t.Fatal("查询应设置 RecursionDesired")
	}

	name, _ := dnsmessage.NewName("example.com.")
	resp := dnsmessage.Message{
		Header: dnsmessage.Header{ID: id, Response: true, RCode: dnsmessage.RCodeSuccess},
		Answers: []dnsmessage.Resource{{
			Header: dnsmessage.ResourceHeader{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 30},
			Body:   &dnsmessage.AResource{A: [4]byte{8, 8, 8, 8}},
		}},
	}
	respPacked, _ := resp.Pack()
	ips, ttl, truncated, err := parseResponse(respPacked, id, "example.com")
	if err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if truncated || len(ips) != 1 || ips[0].String() != "8.8.8.8" || ttl != 30*time.Second {
		t.Fatalf("响应解析错误: ips=%v ttl=%v truncated=%v", ips, ttl, truncated)
	}
	// ID 不匹配应报错
	if _, _, _, err := parseResponse(respPacked, id+1, "example.com"); err == nil {
		t.Fatal("ID 不匹配应报错")
	}
}

func TestClientGetAndGzip(t *testing.T) {
	payload := []byte("hello mproxy")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("下载请求应带 User-Agent")
		}
		switch r.URL.Path {
		case "/text":
			_, _ = w.Write(payload)
		case "/redirect":
			http.Redirect(w, r, "/text", http.StatusFound)
		case "/error":
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewWithResolver(NewResolver()) // 127.0.0.1 走 IP 直连
	ctx := context.Background()
	data, err := c.Get(ctx, srv.URL+"/text", nil, 1024)
	if err != nil || string(data) != string(payload) {
		t.Fatalf("Get 失败: %q %v", data, err)
	}
	data, err = c.Get(ctx, srv.URL+"/redirect", nil, 1024)
	if err != nil || string(data) != string(payload) {
		t.Fatalf("重定向下载失败: %q %v", data, err)
	}
	if _, err := c.Get(ctx, srv.URL+"/error", nil, 1024); err == nil {
		t.Fatal("HTTP 404 应返回错误")
	}
	// gzip 工具函数
	gz, err := gzipBytes(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !IsGzip(gz) {
		t.Fatal("IsGzip 应识别 gzip 数据")
	}
	out, err := Gunzip(gz, 1024)
	if err != nil || string(out) != string(payload) {
		t.Fatalf("Gunzip 失败: %q %v", out, err)
	}
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
