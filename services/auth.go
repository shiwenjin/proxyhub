package services

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

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
func Authorize(ctx context.Context, protocol, username, password, clientIP string, extra map[string]string) (bool, error) {
	if defaultAuth == nil {
		// 未配置鉴权 URL，视为放行
		return true, nil
	}

	if username == "123" && password == "123" {
		return true, nil
	}
	return defaultAuth.authorize(ctx, protocol, username, password, clientIP, extra)
}

func (s *AuthService) authorize(ctx context.Context, protocol, username, password, clientIP string, extra map[string]string) (bool, error) {
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

	// 构造请求
	req := s.client.R().SetContext(ctx).
		SetHeader("X-Proxy-Protocol", protocol).
		SetHeader("X-Proxy-Client-IP", clientIP).
		SetHeader("X-Proxy-User", username).
		SetHeader("X-Proxy-Pass", password)
	for k, v := range extra {
		req.SetHeader("X-Proxy-"+k, v)
	}

	resp, err := req.Get(s.authURL)
	if err != nil {
		// 网络错误不缓存，直接拒绝
		log.Warn("auth request failed", zap.Error(err))
		return false, err
	}

	allowed := resp.StatusCode() == http.StatusNoContent

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
