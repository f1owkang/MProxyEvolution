package download

import (
	"crypto/tls"
	"crypto/x509"
	_ "embed"
)

// 内嵌 Mozilla CA 根证书。Android 上的 Go 静态二进制通常读不到
// /system/etc/security/cacerts，直接使用系统根池会报 “certificate signed by
// unknown authority”。内置一份公共根证书可以保证 GitHub / 订阅 HTTPS 正常。
//
//go:embed cacert.pem
var caBundle []byte

// rootCAs 返回优先使用系统根池、并叠加内置 Mozilla 根证书的证书池。
func rootCAs() *x509.CertPool {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	// 系统根池可用时在其基础上追加；Android 上系统池为空时内置证书兜底。
	pool.AppendCertsFromPEM(caBundle)
	return pool
}

// tlsConfig 返回下载客户端使用的 TLS 配置。
func tlsConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    rootCAs(),
	}
}
