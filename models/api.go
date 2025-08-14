package models

// AuthParams 远程鉴权所需参数
// 对应示例中的：user、pass、client_addr、local_addr、service、sps、target
type AuthParams struct {
	User       string
	Pass       string
	ClientAddr string // IP:port
	LocalAddr  string // IP:port
	Service    string // http|socks
	SPS        bool   // 1|0
	Target     string // http(s) 代理时为完整 URL；socks5 时可为空
}

// AuthResult 远程鉴权返回的配额/路由等信息（从响应头读取）
type AuthResult struct {
	StatusCode    int
	UserConns     int
	IPConns       int
	UserRate      int
	IPRate        int
	UserQPS       int
	IPQPS         int
	Upstream      string
	Outgoing      string
	UserTotalRate int
	IPTotalRate   int
	PortTotalRate int
	RotationTime  int
}
