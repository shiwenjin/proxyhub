package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"proxyhub/models"
	"strconv"
	"strings"

	"github.com/spf13/cast"
	"resty.dev/v3"
)

var DefaultAPI *API

// API 封装了与远程鉴权/上报接口的通信
type API struct {
	client     *resty.Client
	authURL    string
	trafficURL string
}

// NewAPI 创建 API 实例（可选传入 authURL）
func NewAPI(authURL, trafficURL string) *API {
	a := &API{client: resty.New()}
	a.authURL = authURL
	a.trafficURL = trafficURL

	DefaultAPI = a
	return a
}

// Auth 调用远程鉴权接口。成功需返回 HTTP 204。
func (a *API) Auth(ctx context.Context, p models.AuthParams) (bool, models.AuthResult, error) {

	if a.authURL == "" {
		return false, models.AuthResult{}, errors.New("authURL is unavailable")
	}

	var res models.AuthResult
	if a == nil || a.client == nil {
		return false, res, nil
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

	resp, err := req.Get(a.authURL)
	if err != nil {
		return false, res, err
	}

	res.StatusCode = resp.StatusCode()
	if resp.StatusCode() != http.StatusNoContent { // 204
		return false, res, nil
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
	return true, res, nil
}

// Report 上报流量（act=traffic）。要求 204 No Content 视为成功。
func (a *API) Report(rec TrafficRecord) error {
	if a.trafficURL == "" {
		return errors.New("trafficURL is unavailable")
	}

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
		Get(a.trafficURL)
	if err != nil {
		return err
	}

	if resp.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("traffic report failed, status=%d", resp.StatusCode())
	}
	return nil
}
