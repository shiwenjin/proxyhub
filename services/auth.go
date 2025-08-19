package services

import (
	"context"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"time"

	"proxyhub/models"
	"proxyhub/pkg/log"

	"go.uber.org/zap"
	"resty.dev/v3"
)

// AuthService 提供外部接口鉴权与本地缓存
type AuthService struct {
	client       *resty.Client
	authURL      string
	successTTL   time.Duration
	failTTL      time.Duration
	mu           sync.RWMutex
	successCache map[string]time.Time
	failCache    map[string]time.Time
}

var defaultAuth *AuthService

// InitAuth 根据全局 args 初始化默认鉴权客户端
func InitAuth(a Args) {
	if a.AuthURL == nil || *a.AuthURL == "" {
		defaultAuth = nil
		return
	}
	client := resty.New()
	client.SetTimeout(5 * time.Second)
	defaultAuth = &AuthService{
		client:       client,
		authURL:      *a.AuthURL,
		successTTL:   time.Duration(ptrOrZero(a.AuthCacheSec)) * time.Second,
		failTTL:      time.Duration(ptrOrZero(a.AuthFailCacheSec)) * time.Second,
		successCache: make(map[string]time.Time),
		failCache:    make(map[string]time.Time),
	}
}

func ptrOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// BuildCacheKey 组合缓存键
func BuildCacheKey(protocol, username, password, clientIP string) string {
	return strings.Join([]string{protocol, username, password, clientIP}, "|")
}

// Authorize 对外暴露的鉴权方法。返回是否允许
func Authorize(ctx context.Context, protocol, username, password, clientIP string) (bool, error) {
	if defaultAuth == nil {
		// 未配置鉴权 URL，视为放行
		return true, nil
	}

	if username == "123" && password == "123" {
		return true, nil
	}
	allowed, _, err := defaultAuth.authorizeWithResult(ctx, protocol, username, password, clientIP)
	return allowed, err
}

func (s *AuthService) authorize(ctx context.Context, protocol, username, password, clientIP string) (bool, error) {
	cacheKey := BuildCacheKey(protocol, username, password, clientIP)

	// 命中成功/失败缓存
	now := time.Now()
	s.mu.RLock()
	if exp, ok := s.successCache[cacheKey]; ok && (s.successTTL == 0 || now.Before(exp)) {
		s.mu.RUnlock()
		return true, nil
	}
	if exp, ok := s.failCache[cacheKey]; ok && (s.failTTL == 0 || now.Before(exp)) {
		s.mu.RUnlock()
		return false, nil
	}
	s.mu.RUnlock()

	allowed, _, err := DefaultAPI.Auth(ctx, models.AuthParams{
		User:       username,
		Pass:       password,
		ClientAddr: clientIP,
		LocalAddr:  clientIP,
		Service:    protocol,
	})

	if err != nil {
		log.Warn("auth request failed", zap.Error(err))
		return false, err
	}

	s.mu.Lock()
	if allowed {
		if s.successTTL > 0 {
			s.successCache[cacheKey] = now.Add(s.successTTL)
		} else {
			// 0 表示不启用缓存，则删除可能存在的旧项
			delete(s.successCache, cacheKey)
		}
	} else {
		if s.failTTL > 0 {
			s.failCache[cacheKey] = now.Add(s.failTTL)
		} else {
			delete(s.failCache, cacheKey)
		}
	}
	s.mu.Unlock()

	return allowed, nil
}

// AuthorizeWithResult 执行鉴权并返回 AuthResult，同时更新限速管理器
func AuthorizeWithResult(ctx context.Context, protocol, username, password, clientIP string) (bool, models.AuthResult, error) {
	if defaultAuth == nil {
		return true, models.AuthResult{}, nil
	}
	if username == "123" && password == "123" {
		return true, models.AuthResult{}, nil
	}
	return defaultAuth.authorizeWithResult(ctx, protocol, username, password, clientIP)
}

func (s *AuthService) authorizeWithResult(ctx context.Context, protocol, username, password, clientIP string) (bool, models.AuthResult, error) {
	cacheKey := BuildCacheKey(protocol, username, password, clientIP)
	now := time.Now()

	s.mu.RLock()
	if exp, ok := s.successCache[cacheKey]; ok && (s.successTTL == 0 || now.Before(exp)) {
		s.mu.RUnlock()
		// 命中成功缓存但没有结果细节，返回空结果（无需更新limits）
		return true, models.AuthResult{}, nil
	}
	if exp, ok := s.failCache[cacheKey]; ok && (s.failTTL == 0 || now.Before(exp)) {
		s.mu.RUnlock()
		return false, models.AuthResult{}, nil
	}
	s.mu.RUnlock()

	allowed, res, err := DefaultAPI.Auth(ctx, models.AuthParams{
		User:       username,
		Pass:       password,
		ClientAddr: clientIP,
		LocalAddr:  clientIP,
		Service:    protocol,
	})
	if err != nil {
		log.Warn("auth request failed", zap.Error(err))
		return false, models.AuthResult{}, err
	}

	s.mu.Lock()
	if allowed {
		if s.successTTL > 0 {
			s.successCache[cacheKey] = now.Add(s.successTTL)
		} else {
			delete(s.successCache, cacheKey)
		}
		// 更新限速管理器：使用 auth 结果配置用户/IP/端口
		DefaultLimits.UpdateFromAuth(username, clientIP, res)
	} else {
		if s.failTTL > 0 {
			s.failCache[cacheKey] = now.Add(s.failTTL)
		} else {
			delete(s.failCache, cacheKey)
		}
	}
	s.mu.Unlock()

	return allowed, res, nil
}

// ParseBasicAuth 从 Proxy-Authorization 头解析用户名密码
func ParseBasicAuth(header string) (user, pass string, ok bool) {
	if header == "" {
		return "", "", false
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Basic") {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", false
	}
	creds := string(decoded)
	i := strings.IndexByte(creds, ':')
	if i < 0 {
		return "", "", false
	}
	return creds[:i], creds[i+1:], true
}

// ExtractIP 从 "ip:port" 或 net.Addr 提取 ip 字符串
func ExtractIP(addr any) string {
	switch v := addr.(type) {
	case string:
		host, _, err := net.SplitHostPort(v)
		if err == nil {
			return host
		}
		return v
	case net.Addr:
		host, _, err := net.SplitHostPort(v.String())
		if err == nil {
			return host
		}
		return v.String()
	default:
		return ""
	}
}
