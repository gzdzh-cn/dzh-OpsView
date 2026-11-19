package logic

import (
	"context"
	"errors"
	"opsview/internal/model"
)

func (a *App) BackupStorage(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	r, err := a.Store.Resource(id)
	if err != nil || r.Kind != "backup" || r.Config.String("destination") != "s3" {
		return "", errors.New("S3 备份存储不存在")
	}
	data, err := a.Executor.Execute(ctx, model.Operation{Name: "archive.storage-register", Resource: r}, func(string) {})
	if err != nil {
		return "", err
	}
	return data.String("storageId"), nil
}
