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
	StatusCode int // 状态码
	UserId     int // 用户ID
	TeamId     int // 团队ID
	// UserConns     int    // 用户的最大连接数
	// IPConns       int    // IP的最大连接数
	// UserRate int // 用户的单个TCP连接速率限制，单位：字节/秒，不限制为0或者不设置这个头部
	// IPRate        int    // IP的单个TCP连接速率限制，单位：字节/秒，不限制为0或者不设置这个头部。
	UserQPS int `json:"userQps" default:"100"` // 用户每秒可以建立的最大连接数，不限制为0或者不设置这个头部 100/s
	// IPQPS         int    // IP每秒可以建立的最大连接数，不限制为0或者不设置这个头部。
	// Upstream      string // 使用的上级，没有为空，或者不设置这个头部。
	// Outgoing      string // 使用的出口IP
	UserTotalRate int `json:"userTotalRate" default:"2"` // 用户维度，限制用户的总带宽速度（byte/s），单位是字节byte，没有留空，或者不设置这个头部。 2Mbps/s
	// IPTotalRate   int    // 客户端IP维度，限制客户端IP的总带宽速度（byte/s），单位是字节byte，没有留空，或者不设置这个头部
	// PortTotalRate int    // 端口维度，限制一个端口的总带宽速度（byte/s），单位是字节byte，没有留空，或者不设置这个头部。
	// RotationTime  int    // 轮换时间
}

// UserTotalRate Mbps/s 转化为 byte/s
func (a *AuthResult) UserTotalRateToByte() int {
	return a.UserTotalRate * 1024 * 1024 / 8
}
