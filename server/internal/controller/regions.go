package controller

import (
	"context"
	"errors"
	"net/http"
	"opsview/internal/model"
	"time"
)

func (c *Controller) regionStatus(ctx context.Context, ip string) (model.Config, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.App.Executor.Execute(ctx, model.Operation{Name: "system.panel-region-status", Params: model.Config{"ip": ip}}, func(string) {})
}
func (c *Controller) checkRegion(r *http.Request, p model.Config) error {
	var countries []string
	raw := p["loginCountries"]
	switch v := raw.(type) {
	case []string:
		countries = v
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				countries = append(countries, s)
			}
		}
	}
	if len(countries) == 0 {
		return nil
	}
	status, err := c.regionStatus(r.Context(), clientIP(r))
	if err != nil || !status.Bool("available") {
		return errors.New("登录地区数据不可用，请更新本地数据或通过 ov security recover 恢复")
	}
	country := status.String("country")
	if country == "" && p.String("unknownCountry") == "allow" {
		return nil
	}
	for _, allowed := range countries {
		if allowed == country {
			return nil
		}
	}
	return errors.New("当前登录来源地区不在允许范围内")
}
func (c *Controller) regions(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		result, err := c.regionStatus(r.Context(), clientIP(r))
		if err != nil {
			fail(w, 502, err.Error())
			return
		}
		send(w, 200, result)
		return
	}
	task, err := c.App.Store.Enqueue(model.Operation{Name: "system.panel-region-update", Resource: model.Resource{ID: "panel-region-data", Kind: "system", Name: "面板地区地址数据"}, Params: model.Config{}})
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	c.App.Store.Audit("regions.update", "", clientIP(r))
	send(w, 202, task)
}
