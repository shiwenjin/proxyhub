package services

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"proxyhub/models"

	"golang.org/x/time/rate"
)

// Limit identifiers and context keys
type contextKey string

const (
	ctxKeyUsername contextKey = "ph.username"
	ctxKeyClientIP contextKey = "ph.clientip"
	ctxKeyProtocol contextKey = "ph.protocol"
	ctxKeyUserId   contextKey = "ph.userid"
	ctxKeyTeamId   contextKey = "ph.teamid"
)

// SetAuthInfoOnContext attaches auth-related info for later dial/limiting usage
func SetAuthInfoOnContext(ctx context.Context, protocol, username, clientIP string, userId, teamId int) context.Context {
	if ctx == nil {
		return context.Background()
	}
	ctx = context.WithValue(ctx, ctxKeyProtocol, protocol)
	ctx = context.WithValue(ctx, ctxKeyUsername, username)
	ctx = context.WithValue(ctx, ctxKeyClientIP, clientIP)
	ctx = context.WithValue(ctx, ctxKeyUserId, userId)
	ctx = context.WithValue(ctx, ctxKeyTeamId, teamId)
	return ctx
}

// ExtractAuthInfo extracts protocol/username/clientIP/userId/teamId from context
func ExtractAuthInfo(ctx context.Context) (username, clientIP string, userId, teamId int) {
	if v := ctx.Value(ctxKeyUsername); v != nil {
		if s, ok := v.(string); ok {
			username = s
		}
	}
	if v := ctx.Value(ctxKeyClientIP); v != nil {
		if s, ok := v.(string); ok {
			clientIP = s
		}
	}
	if v := ctx.Value(ctxKeyUserId); v != nil {
		if id, ok := v.(int); ok {
			userId = id
		}
	}
	if v := ctx.Value(ctxKeyTeamId); v != nil {
		if id, ok := v.(int); ok {
			teamId = id
		}
	}
	return
}

// entityKey represents a logical limiter scope
type entityKey struct {
	kind string // user|ip|port
	id   string // username or ip or port string
}

type entityState struct {
	totalRateLimiter *rate.Limiter     // 总带宽限制
	qpsLimiter       *rate.Limiter     // QPS限制
	lastResult       models.AuthResult // 鉴权结果
}

// LimitsManager manages shared limiters and state across connections
type LimitsManager struct {
	mu    sync.Mutex
	items map[entityKey]*entityState
}

var DefaultLimits = &LimitsManager{items: make(map[entityKey]*entityState)}
var defaultLimit = 250000 // byte/s
var defaultQPSLimit = 1   // qps

func (m *LimitsManager) getOrCreate(key entityKey) *entityState {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.items[key]
	if !ok {
		if key.kind == "user" && key.id != "" {
			st = &entityState{totalRateLimiter: rate.NewLimiter(rate.Limit(float64(defaultLimit)), defaultLimit),
				qpsLimiter: rate.NewLimiter(rate.Limit(float64(defaultQPSLimit)), defaultQPSLimit)}
		} else if key.kind == "ip" && key.id != "" {
			st = &entityState{totalRateLimiter: rate.NewLimiter(rate.Limit(float64(defaultLimit)), defaultLimit),
				qpsLimiter: rate.NewLimiter(rate.Limit(float64(defaultQPSLimit)), defaultQPSLimit)}
		} else {
			st = &entityState{}
		}
		m.items[key] = st
	}
	return st
}

// UpdateFromAuth updates limiters based on latest AuthResult.
// protocol used for scoping; local listens at args.Local (port).
func (m *LimitsManager) UpdateFromAuth(username, clientIP string, res models.AuthResult) {
	// User level
	if strings.TrimSpace(username) != "" {
		st := m.getOrCreate(entityKey{kind: "user", id: username})
		st.lastResult = res
		if res.UserTotalRate > 0 {
			// 转换为字节/秒
			bytesPerSec := res.UserTotalRateToByte()
			st.totalRateLimiter = rate.NewLimiter(rate.Limit(float64(bytesPerSec)), bytesPerSec)
		} else {
			st.totalRateLimiter = nil
		}
		if res.UserQPS > 0 {
			st.qpsLimiter = rate.NewLimiter(rate.Limit(float64(res.UserQPS)), res.UserQPS)
		} else {
			st.qpsLimiter = nil
		}
	}

}

// 检查用户/IP的QPS限制
func (m *LimitsManager) AllowQPS(username, clientIP string) bool {
	// user
	if strings.TrimSpace(username) != "" {
		st := m.getOrCreate(entityKey{kind: "user", id: username})
		if st.qpsLimiter != nil {
			if !st.qpsLimiter.Allow() {
				return false
			}
		}
	}
	// ip
	if strings.TrimSpace(clientIP) != "" {
		st := m.getOrCreate(entityKey{kind: "ip", id: clientIP})
		if st.qpsLimiter != nil {
			if !st.qpsLimiter.Allow() {
				return false
			}
		}
	}
	return true
}

// 构建连接限速器
func (m *LimitsManager) BuildConnLimiters(username, clientIP string) (shared []*rate.Limiter) {

	// 总带宽：用户、IP
	if strings.TrimSpace(username) != "" {
		st := m.getOrCreate(entityKey{kind: "user", id: username})
		if st.totalRateLimiter != nil {
			shared = append(shared, st.totalRateLimiter)
		}
	}
	if strings.TrimSpace(clientIP) != "" {
		st := m.getOrCreate(entityKey{kind: "ip", id: clientIP})
		if st.totalRateLimiter != nil {
			shared = append(shared, st.totalRateLimiter)
		}
	}
	return
}

// LimiterConn wraps a connection and throttles after each IO using the provided limiters
type LimiterConn struct {
	net.Conn
	ctx        context.Context
	sharedLims []*rate.Limiter
}

func NewLimiterConn(ctx context.Context, c net.Conn, shared []*rate.Limiter) *LimiterConn {
	return &LimiterConn{Conn: c, ctx: ctx, sharedLims: shared}
}

func (l *LimiterConn) throttle(n int) error {
	if n <= 0 {
		return nil
	}

	// 设置一个短期的超时上下文，例如100毫秒。
	// 在这个时间内，我们愿意等待，超过则直接拒绝。
	waitCtx, cancel := context.WithTimeout(l.ctx, 100*time.Millisecond)
	defer cancel()

	// 总带宽限速器 - 限制用户/IP的总带宽
	for i := range l.sharedLims {
		if l.sharedLims[i] != nil {
			err := l.sharedLims[i].WaitN(waitCtx, n)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (l *LimiterConn) Read(b []byte) (int, error) {
	n, err := l.Conn.Read(b)
	if n > 0 {
		if throttleErr := l.throttle(n); throttleErr != nil {
			// 如果限速失败，返回错误
			return 0, throttleErr
		}
	}
	return n, err
}

func (l *LimiterConn) Write(b []byte) (int, error) {
	n, err := l.Conn.Write(b)
	if n > 0 {
		if throttleErr := l.throttle(n); throttleErr != nil {
			// 如果限速失败，返回错误
			return 0, throttleErr
		}
	}
	return n, err
}
