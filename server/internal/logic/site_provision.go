package logic

import (
	"context"
	"fmt"
	"opsview/internal/model"
)

func (a *App) provisionSite(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	site, err := a.Store.Resource(op.Resource.ID)
	if err != nil {
		return nil, err
	}
	if id := site.Config.String("databaseId"); id != "" {
		db, er := a.Store.Resource(id)
		if er != nil {
			return nil, er
		}
		if db.Status != "ready" {
			log("1/2 创建或核对关联数据库（凭据不写入日志）")
			data, er := a.Executor.Execute(ctx, model.Operation{Name: "database.create", Resource: db, Params: model.Config{"resume": true}}, func(string) {})
			if er != nil {
				return nil, fmt.Errorf("数据库步骤失败，资源保留供重试：%w", er)
			}
			for k, v := range data {
				db.Config[k] = v
			}
			db.Status = "ready"
			if er = a.Store.Update(&db); er != nil {
				return nil, er
			}
		} else {
			log("1/2 复用已成功创建的关联数据库")
		}
	}
	log("2/2 部署站点并验证访问")
	name := "site.apply"
	if op.Params.String("certificate") != "" || op.Params.String("privateKey") != "" {
		name = "site.certificate"
	}
	return a.Executor.Execute(ctx, model.Operation{Name: name, Resource: site, Params: op.Params}, log)
}
