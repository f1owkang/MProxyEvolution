package download

import (
	"bytes"
	"crypto/tls"
	"net/http"
	"testing"
)

func TestEmbeddedCARoots(t *testing.T) {
	if len(caBundle) == 0 {
		t.Fatal("嵌入式 CA 证书为空")
	}
	if !bytes.Contains(caBundle, []byte("BEGIN CERTIFICATE")) {
		t.Fatal("cacert.pem 不是 PEM 证书")
	}
	pool := rootCAs()
	if pool == nil {
		t.Fatal("rootCAs 返回 nil")
	}
	if subjects := pool.Subjects(); len(subjects) == 0 {
		t.Fatal("根证书池没有加载任何证书")
	}
}

func TestTLSConfigUsesEmbeddedRoots(t *testing.T) {
	cfg := tlsConfig()
	if cfg == nil {
		t.Fatal("tlsConfig 返回 nil")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %x，期望 TLS 1.2", cfg.MinVersion)
	}
	if cfg.RootCAs == nil {
		t.Fatal("TLS RootCAs 未设置，Android 上会再次出现 unknown authority")
	}
}

func TestDownloadClientTLSConfig(t *testing.T) {
	client := New()
	tr, ok := client.HTTP.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport 类型 = %T", client.HTTP.Transport)
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.RootCAs == nil {
		t.Fatal("下载客户端未使用带根证书的 TLS 配置")
	}
}
