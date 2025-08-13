package services

import (
	"context"
	"fmt"
	"net/http"
	"proxyhub/models"
	"strconv"
	"strings"

	"github.com/spf13/cast"
	"resty.dev/v3"
)

var api = NewAPI("http://127.0.0.1:8085")

// API 封装了与远程鉴权/上报接口的通信
type API struct {
	client *resty.Client
}

// NewAPI 创建 API 实例（可选传入 authURL）
func NewAPI(baseURL ...string) *API {
	a := &API{client: resty.New()}
	if len(baseURL) > 0 {
		a.client.SetBaseURL(baseURL[0])
	}
	return a
}

// Auth 调用远程鉴权接口。成功需返回 HTTP 204。
func (a *API) Auth(ctx context.Context, p models.AuthParams) (models.AuthResult, error) {
	var res models.AuthResult
	if a == nil || a.client == nil {
		return res, nil
	}

	req := a.client.R().
		SetContext(ctx).
		SetQueryParam("user", p.User).
		SetQueryParam("pass", p.Pass).
		SetQueryParam("clientAddr", p.ClientAddr).
		SetQueryParam("localAddr", p.LocalAddr).
		SetQueryParam("service", p.Service).
		SetQueryParam("sps", func() string {
			if p.SPS {
				return "1"
			}
			return "0"
		}())

	if strings.TrimSpace(p.Target) != "" {
		req = req.SetQueryParam("target", p.Target)
	}

	resp, err := req.Get("/selfproxy/auth")
	if err != nil {
		return res, err
	}

	res.StatusCode = resp.StatusCode()
	if resp.StatusCode() != http.StatusNoContent { // 204
		return res, nil
	}

	// 解析响应头
	h := resp.Header()
	res.UserConns = cast.ToInt(h.Get("userconns"))
	res.IPConns = cast.ToInt(h.Get("ipconns"))
	res.UserRate = cast.ToInt(h.Get("userrate"))
	res.IPRate = cast.ToInt(h.Get("iprate"))
	res.UserQPS = cast.ToInt(h.Get("userqps"))
	res.IPQPS = cast.ToInt(h.Get("ipqps"))
	res.Upstream = strings.TrimSpace(h.Get("upstream"))
	res.Outgoing = strings.TrimSpace(h.Get("outgoing"))
	res.UserTotalRate = cast.ToInt(h.Get("userTotalRate"))
	res.IPTotalRate = cast.ToInt(h.Get("ipTotalRate"))
	res.PortTotalRate = cast.ToInt(h.Get("portTotalRate"))
	res.RotationTime = cast.ToInt(h.Get("RotationTime"))
	return res, nil
}

// Report 上报流量（act=traffic）。要求 204 No Content 视为成功。
func (a *API) Report(rec TrafficRecord) error {
	if a == nil || a.client == nil {
		return nil
	}

	params := map[string]string{
		"act":           "traffic",
		"id":            rec.ID,
		"serverAddr":    rec.ServerAddr,
		"clientAddr":    rec.ClientAddr,
		"bytes":         strconv.FormatInt(rec.Bytes, 10),
		"outLocalAddr":  rec.OutLocalAddr,
		"outRemoteAddr": rec.OutRemoteAddr,
		"upstream":      rec.Upstream,
	}
	if strings.TrimSpace(rec.TargetAddr) != "" {
		params["targetAddr"] = rec.TargetAddr
	}
	if strings.TrimSpace(rec.Username) != "" {
		params["username"] = rec.Username
	}
	if strings.TrimSpace(rec.SniffDomain) != "" {
		params["sniffDomain"] = rec.SniffDomain
	}

	resp, err := a.client.R().
		SetQueryParams(params).
		Get("/selfproxy/report")
	if err != nil {
		return err
	}

	if resp.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("traffic report failed, status=%d", resp.StatusCode())
	}
	return nil
}
