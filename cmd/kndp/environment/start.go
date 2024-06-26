package environment

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/kndpio/kndp/internal/environment"
)

type startCmd struct {
	Name   string `arg:"" required:"" help:"Name of environment."`
	Switch bool   `optional:"" short:"s" help:"Switch kubernetes context to started cluster context."`
	Engine string `arg:"" required:"" help:"Specifies the Kubernetes engine to use for the runtime environment." default:"kind"`
}

func (c *startCmd) Run(ctx context.Context, logger *log.Logger) error {
	return environment.Start(ctx, c.Name, c.Switch, c.Engine, logger)
}
