package configuration

import (
	"context"

	"github.com/kndpio/kndp/internal/configuration"

	"k8s.io/client-go/rest"

	"github.com/charmbracelet/log"
)

type deleteCmd struct {
	ConfigurationURL string `arg:"" required:"" help:"Specifies the URL of configuration to be deleted from Environment."`
}

func (c *deleteCmd) Run(ctx context.Context, config *rest.Config, logger *log.Logger) error {
	return configuration.DeleteConfiguration(c.ConfigurationURL, config, logger)
}
