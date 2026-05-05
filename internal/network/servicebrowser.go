package network

import (
	"context"
	"net"
)

// ServiceEntry represents a discovered mDNS/DNS-SD service instance.
// It is the platform-independent representation that replaces
// github.com/hashicorp/mdns.ServiceEntry.
type ServiceEntry struct {
	Name       string
	AddrV4     net.IP
	AddrV6     net.IP
	Port       int
	InfoFields []string
}

// ServiceHandle represents a registered mDNS/DNS-SD service.
// It can be shut down to deregister the service.
type ServiceHandle interface {
	Shutdown() error
}

// ServiceBrowser abstracts mDNS/DNS-SD service discovery and advertisement.
// Platform-specific implementations are provided via build tags:
//   - servicebrowser_unix.go:    Linux/macOS using github.com/hashicorp/mdns
//   - servicebrowser_windows.go: Windows (currently hashicorp/mdns stub
//     with explicit Interface workaround; future dnsapi.dll native impl TBD)
type ServiceBrowser interface {
	// Browse performs a one-shot mDNS query for the given service/domain.
	// Returns discovered entries. Callers should invoke Browse periodically
	// (e.g., every 5 seconds) to maintain up-to-date peer discovery.
	Browse(ctx context.Context, service string, domain string, iface *net.Interface) ([]*ServiceEntry, error)

	// Register advertises a service on the local network.
	Register(name, service, domain string, port int, ips []net.IP, info []string, iface *net.Interface) (ServiceHandle, error)
}

// NewServiceBrowser returns the platform-appropriate ServiceBrowser.
var NewServiceBrowser func() ServiceBrowser