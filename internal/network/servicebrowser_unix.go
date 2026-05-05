//go:build !windows

package network

import (
	"context"
	"net"
	"time"

	"github.com/hashicorp/mdns"
)

const queryInterval = 5 * time.Second

type mdnsBrowser struct{}

func init() {
	NewServiceBrowser = func() ServiceBrowser {
		return &mdnsBrowser{}
	}
}

func (b *mdnsBrowser) Browse(ctx context.Context, service string, domain string, iface *net.Interface) ([]*ServiceEntry, error) {
	entriesCh := make(chan *mdns.ServiceEntry, 32)

	params := &mdns.QueryParam{
		Service:             service,
		Domain:              domain,
		Timeout:             5 * queryInterval,
		Entries:             entriesCh,
		WantUnicastResponse: false,
		Interface:           iface,
	}

	go func() {
		if err := mdns.Query(params); err != nil {
			return
		}
		close(entriesCh)
	}()

	var results []*ServiceEntry
	for entry := range entriesCh {
		if entry == nil {
			continue
		}
		results = append(results, &ServiceEntry{
			Name:       entry.Name,
			AddrV4:     entry.AddrV4,
			AddrV6:     entry.AddrV6,
			Port:       entry.Port,
			InfoFields: entry.InfoFields,
		})
	}
	return results, nil
}

func (b *mdnsBrowser) Register(name, service, domain string, port int, ips []net.IP, info []string, iface *net.Interface) (ServiceHandle, error) {
	serviceInstance, err := mdns.NewMDNSService(
		name,
		service,
		domain,
		"",
		port,
		ips,
		info,
	)
	if err != nil {
		return nil, err
	}

	cfg := &mdns.Config{Zone: serviceInstance}
	if iface != nil {
		cfg.Iface = iface
	}

	server, err := mdns.NewServer(cfg)
	if err != nil {
		return nil, err
	}

	return &mdnsHandle{server: server}, nil
}

type mdnsHandle struct {
	server *mdns.Server
}

func (h *mdnsHandle) Shutdown() error {
	return h.server.Shutdown()
}
