package service

import (
	"context"
	"opsview/internal/model"
)

// Executor is deliberately narrower than a shell. Both transports use the same
// operation validation in the privileged process.
type Executor interface {
	Execute(context.Context, model.Operation, func(string)) (model.Config, error)
}
