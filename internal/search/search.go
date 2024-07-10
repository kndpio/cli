package search

import (
	"context"
	"net/url"
	"strings"

	"github.com/pterm/pterm"
	"go.uber.org/zap"

	"github.com/kndpio/kndp/internal/github"
	"github.com/kndpio/kndp/internal/registry"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func SearchPackages(ctx context.Context, client *kubernetes.Clientset, config *rest.Config, query string, versions bool, logger *zap.Logger) (pterm.TableData, error) {
	registries, err := registry.Registries(ctx, client)
	if err != nil {
		logger.Sugar().Error("Cannot get registries")
		return nil, err
	}

	for _, r := range registries {
		registryUrl := r.Annotations["kndp-registry-server-url"]
		u, _ := url.Parse(registryUrl)
		org := strings.TrimPrefix(u.Path, "/")
		// Switch statement to handle different registry types
		switch {
		case strings.Contains(registryUrl, "ghcr.io"):
			tableRegs, err := github.GetPackages(ctx, query, versions, r, registryUrl, org, logger)
			if err != nil {
				return nil, err
			}
			return tableRegs, nil
		default:
			logger.Sugar().Errorf("Registry %s is not supported", registryUrl)
		}

	}
	return nil, err
}
