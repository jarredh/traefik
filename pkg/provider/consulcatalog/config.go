package consulcatalog

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/containous/traefik/v2/pkg/config/dynamic"
	"github.com/containous/traefik/v2/pkg/config/label"
	"github.com/containous/traefik/v2/pkg/log"
	"github.com/containous/traefik/v2/pkg/provider"
	"github.com/containous/traefik/v2/pkg/provider/constraints"
	"github.com/hashicorp/consul/api"
)

func (p *Provider) buildConfiguration(ctx context.Context, items []itemData) *dynamic.Configuration {
	configurations := make(map[string]*dynamic.Configuration)

	for _, item := range items {
		svcName := item.Node + "-" + item.Name + "-" + item.ID
		ctxSvc := log.With(ctx, log.Str("serviceName", svcName))

		if !p.keepContainer(ctxSvc, item) {
			continue
		}

		logger := log.FromContext(ctxSvc)

		confFromLabel, err := label.DecodeConfiguration(item.Labels)
		if err != nil {
			logger.Error(err)
			continue
		}

		if len(confFromLabel.TCP.Routers) > 0 || len(confFromLabel.TCP.Services) > 0 {
			err := p.buildTCPServiceConfiguration(ctxSvc, item, confFromLabel.TCP)
			if err != nil {
				logger.Error(err)
				continue
			}

			provider.BuildTCPRouterConfiguration(ctxSvc, confFromLabel.TCP)

			if len(confFromLabel.HTTP.Routers) == 0 &&
				len(confFromLabel.HTTP.Middlewares) == 0 &&
				len(confFromLabel.HTTP.Services) == 0 {
				configurations[svcName] = confFromLabel
				continue
			}
		}

		err = p.buildServiceConfiguration(ctxSvc, item, confFromLabel.HTTP)
		if err != nil {
			logger.Error(err)
			continue
		}

		model := struct {
			Name   string
			Labels map[string]string
		}{
			Name:   item.Name,
			Labels: item.Labels,
		}

		provider.BuildRouterConfiguration(ctx, confFromLabel.HTTP, item.Name, p.defaultRuleTpl, model)

		configurations[svcName] = confFromLabel
	}

	return provider.Merge(ctx, configurations)
}

func (p *Provider) keepContainer(ctx context.Context, item itemData) bool {
	logger := log.FromContext(ctx)

	if !item.ExtraConf.Enable {
		logger.Debug("Filtering disabled item")
		return false
	}

	matches, err := constraints.MatchTags(item.Tags, p.Constraints)
	if err != nil {
		logger.Errorf("Error matching constraints expression: %v", err)
		return false
	}
	if !matches {
		logger.Debugf("Container pruned by constraint expression: %q", p.Constraints)
		return false
	}

	if item.Status != api.HealthPassing && item.Status != api.HealthWarning {
		logger.Debug("Filtering unhealthy or starting item")
		return false
	}

	return true
}

func (p *Provider) buildTCPServiceConfiguration(ctx context.Context, item itemData, configuration *dynamic.TCPConfiguration) error {
	if len(configuration.Services) == 0 {
		configuration.Services = make(map[string]*dynamic.TCPService)

		lb := &dynamic.TCPServersLoadBalancer{}
		lb.SetDefaults()

		configuration.Services[item.Name] = &dynamic.TCPService{
			LoadBalancer: lb,
		}
	}

	for name, service := range configuration.Services {
		ctxSvc := log.With(ctx, log.Str(log.ServiceName, name))
		err := p.addServerTCP(ctxSvc, item, service.LoadBalancer)
		if err != nil {
			return err
		}
	}

	return nil
}

func (p *Provider) buildServiceConfiguration(ctx context.Context, item itemData, configuration *dynamic.HTTPConfiguration) error {
	if len(configuration.Services) == 0 {
		configuration.Services = make(map[string]*dynamic.Service)

		lb := &dynamic.ServersLoadBalancer{}
		lb.SetDefaults()

		configuration.Services[item.Name] = &dynamic.Service{
			LoadBalancer: lb,
		}
	}

	for name, service := range configuration.Services {
		ctxSvc := log.With(ctx, log.Str(log.ServiceName, name))
		err := p.addServer(ctxSvc, item, service.LoadBalancer)
		if err != nil {
			return err
		}
	}

	return nil
}

func (p *Provider) addServerTCP(ctx context.Context, item itemData, loadBalancer *dynamic.TCPServersLoadBalancer) error {
	if loadBalancer == nil {
		return errors.New("load-balancer is not defined")
	}

	if len(loadBalancer.Servers) == 0 {
		loadBalancer.Servers = []dynamic.TCPServer{{}}
	}

	var port string
	if item.Port != "" {
		port = item.Port
		loadBalancer.Servers[0].Port = ""
	}

	if port == "" {
		return errors.New("port is missing")
	}

	if item.Address == "" {
		return errors.New("address is missing")
	}

	loadBalancer.Servers[0].Address = net.JoinHostPort(item.Address, port)
	return nil
}

func (p *Provider) addServer(ctx context.Context, item itemData, loadBalancer *dynamic.ServersLoadBalancer) error {
	if loadBalancer == nil {
		return errors.New("load-balancer is not defined")
	}

	var port string
	if len(loadBalancer.Servers) > 0 {
		port = loadBalancer.Servers[0].Port
	}

	if len(loadBalancer.Servers) == 0 {
		server := dynamic.Server{}
		server.SetDefaults()

		loadBalancer.Servers = []dynamic.Server{server}
	}

	if item.Port != "" {
		port = item.Port
		loadBalancer.Servers[0].Port = ""
	}

	if port == "" {
		return errors.New("port is missing")
	}

	if item.Address == "" {
		return errors.New("address is missing")
	}

	loadBalancer.Servers[0].URL = fmt.Sprintf("%s://%s", loadBalancer.Servers[0].Scheme, net.JoinHostPort(item.Address, port))
	loadBalancer.Servers[0].Scheme = ""

	return nil
}
