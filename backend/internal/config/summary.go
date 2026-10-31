package config

// Summary 是配置摘要，供状态接口与面板展示。
type Summary struct {
	Mode          string `json:"mode"`
	LogLevel      string `json:"logLevel"`
	MixedPort     int    `json:"mixedPort"`
	SocksPort     int    `json:"socksPort"`
	Port          int    `json:"port"`
	AllowLan      bool   `json:"allowLan"`
	IPv6          bool   `json:"ipv6"`
	TunEnabled    bool   `json:"tunEnabled"`
	TunStack      string `json:"tunStack"`
	ProxyMode     string `json:"proxyMode"`
	AutoStart     bool   `json:"autoStart"`
	ProxyCount    int    `json:"proxyCount"`
	GroupCount    int    `json:"groupCount"`
	RuleCount     int    `json:"ruleCount"`
	ProviderCount int    `json:"providerCount"`
}

// Summary 生成当前配置的摘要。
func (d *Doc) Summary() Summary {
	s := Summary{
		Mode:          d.GetString("rule", "mode"),
		LogLevel:      d.GetString("info", "log-level"),
		MixedPort:     d.GetInt(0, "mixed-port"),
		SocksPort:     d.GetInt(0, "socks-port"),
		Port:          d.GetInt(0, "port"),
		AllowLan:      d.GetBool(false, "allow-lan"),
		IPv6:          d.GetBool(false, "ipv6"),
		TunEnabled:    d.GetBool(false, "tun", "enable"),
		TunStack:      d.GetString("mixed", "tun", "stack"),
		ProxyMode:     d.Settings().ProxyMode,
		AutoStart:     d.Settings().AutoStart,
		ProxyCount:    d.listLen("proxies"),
		GroupCount:    d.listLen("proxy-groups"),
		RuleCount:     d.listLen("rules"),
		ProviderCount: d.mapLen("proxy-providers"),
	}
	return s
}

func (d *Doc) listLen(key string) int {
	v, ok := d.Get(key)
	if !ok {
		return 0
	}
	if l, ok := v.([]any); ok {
		return len(l)
	}
	return 0
}

func (d *Doc) mapLen(key string) int {
	v, ok := d.Get(key)
	if !ok {
		return 0
	}
	if m, ok := v.(map[string]any); ok {
		return len(m)
	}
	return 0
}

// ProxyMode 返回 x-mproxy.proxy-mode（默认 tun）。
func (d *Doc) ProxyMode() string { return d.Settings().ProxyMode }

// Ports 返回 (mixed, socks, port)。
func (d *Doc) Ports() (int, int, int) {
	return d.GetInt(0, "mixed-port"), d.GetInt(0, "socks-port"), d.GetInt(0, "port")
}
