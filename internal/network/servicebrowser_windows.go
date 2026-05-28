//go:build windows

package network

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/hashicorp/mdns"
	"parade/internal/core/logger"
)

const queryInterval = 5 * time.Second

// windowsBrowser wraps hashicorp/mdns with the explicit Interface workaround
// for Windows (see github.com/hashicorp/mdns issue #80).
//
// TODO: Replace with native dnsapi.dll DNS-SD implementation using:
//   - DnsServiceBrowse for service discovery
//   - DnsServiceRegister for service advertisement
//   - DnsServiceResolve for service resolution
//
// The dnsapi.dll DNS-SD APIs are asynchronous (return DNS_REQUEST_PENDING and
// invoke callbacks on Windows threads). Implementing them requires:
//   1. syscall.LoadLibrary("dnsapi.dll") + GetProcAddress for each function
//   2. syscall.NewCallback for bridging C callbacks to Go goroutines
//   3. Complex struct marshaling (DNS_SERVICE_BROWSE_REQUEST, DNS_SERVICE_INSTANCE, etc.)
//   4. Careful handling of DNS_REQUEST_PENDING and callback thread safety
//
// This is a non-trivial syscall binding effort best done as a focused task.
type windowsBrowser struct {
	logr logger.Logger
}

func (b *windowsBrowser) WithLogger(l logger.Logger) *windowsBrowser {
	b.logr = l
	return b
}

func (b *windowsBrowser) log(level logger.LogLevel, source, msg string) {
	if b.logr != nil {
		switch level {
		case logger.Trace:
			b.logr.Trace(source, msg)
		case logger.Debug:
			b.logr.Debug(source, msg)
		case logger.Info:
			b.logr.Info(source, msg)
		case logger.Warning:
			b.logr.Warn(source, msg)
		case logger.Error:
			b.logr.Error(source, msg)
		}
	}
}

func init() {
	NewServiceBrowser = func() ServiceBrowser {
		return &windowsBrowser{}
	}
}

func (b *windowsBrowser) Browse(ctx context.Context, service string, domain string, iface *net.Interface) ([]*ServiceEntry, error) {
	entriesCh := make(chan *mdns.ServiceEntry, 32)

	params := &mdns.QueryParam{
		Service:             service,
		Domain:              domain,
		Timeout:             5 * queryInterval,
		Entries:             entriesCh,
		WantUnicastResponse: false,
	}

	// Windows workaround: hashicorp/mdns issue #80 — queries fail on Windows
	// unless params.Interface is explicitly set. If no interface was selected,
	// we still attempt the query (it may work on some configurations).
	if iface != nil {
		params.Interface = iface
	}

	go func() {
		if err := mdns.Query(params); err != nil {
			b.log(logger.Warning, "discovery", fmt.Sprintf("mDNS Windows browse query failed: %v", err))
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

func (b *windowsBrowser) Register(name, service, domain string, port int, ips []net.IP, info []string, iface *net.Interface) (ServiceHandle, error) {
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
		return nil, fmt.Errorf("failed to start mDNS server on Windows: %w", err)
	}

	return &mdnsHandle{server: server}, nil
}

type mdnsHandle struct {
	server *mdns.Server
}

func (h *mdnsHandle) Shutdown() error {
	return h.server.Shutdown()
}
