package services

// tcp := app.Command("tcp", "proxy on tcp mode")
// t := tcp.Flag("tcp-timeout", "tcp timeout milliseconds when connect to real server or parent proxy").Default("2000").Int()

const (
	TYPE_TCP     = "tcp"
	TYPE_UDP     = "udp"
	TYPE_HTTP    = "http"
	TYPE_TLS     = "tls"
	CONN_CONTROL = uint8(1)
	CONN_SERVER  = uint8(2)
	CONN_CLIENT  = uint8(3)

	TRAFFIC_MODE_FAST   = "fast"
	TRAFFIC_MODE_NORMAL = "normal"
)

type Args struct {
	Local            *string // 本地监听地址
	Parent           *string // 父级代理地址
	Verbose          *bool   // 是否打印详细日志
	LogFile          *string // 日志文件路径
	LogWarn          *bool   // 只记录警告日志
	Env              *string // 环境
	BindListen       *bool   // 使用监听IP作为出口IP
	AuthURL          *string // 认证URL
	AuthNoUser       *bool   // 认证不使用用户名密码
	AuthCacheSec     *int    // 认证缓存秒数
	AuthFailCacheSec *int    // 认证失败缓存秒数

	TrafficMode     *string // 流量模式
	FastGlobal      *bool   // 全局快速模式
	TrafficURL      *string // 流量上报URL
	TrafficInterval *int    // 流量间隔
	ServiceID       *string // 服务ID

	CertBytes []byte
	KeyBytes  []byte
}
type TunnelServerArgs struct {
	Args
	IsUDP   *bool
	Key     *string
	Timeout *int
}
type TunnelClientArgs struct {
	Args
	IsUDP   *bool
	Key     *string
	Timeout *int
}
type TunnelBridgeArgs struct {
	Args
	Timeout *int
}
type TCPArgs struct {
	Args
	ParentType          *string
	IsTLS               *bool
	Timeout             *int
	PoolSize            *int
	CheckParentInterval *int
}

type HTTPArgs struct {
	Args
	Always              *bool
	HTTPTimeout         *int
	Interval            *int
	Blocked             *string
	Direct              *string
	AuthFile            *string
	Auth                *[]string
	ParentType          *string
	LocalType           *string
	Timeout             *int
	PoolSize            *int
	CheckParentInterval *int
}
type UDPArgs struct {
	Args
	ParentType          *string
	Timeout             *int
	PoolSize            *int
	CheckParentInterval *int
}

func (a *TCPArgs) Protocol() string {
	if *a.IsTLS {
		return "tls"
	}
	return "tcp"
}
