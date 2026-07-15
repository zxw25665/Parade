package network

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"

	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
)

const ServiceName = "_parade._tcp"

type MDNSService struct {
	host      *libp2pHost
	discovery mdns.Service
	bus       eventbus.EventBus
	logr      logger.Logger

	mu         sync.RWMutex
	started    bool
	closed     bool

	onPeerFound func(peer.AddrInfo)
}

func NewMDNSService(h *libp2pHost, bus eventbus.EventBus, logr logger.Logger) *MDNSService {
	return &MDNSService{
		host: h,
		bus:  bus,
		logr: logr,
	}
}

func (m *MDNSService) SetOnPeerFound(cb func(peer.AddrInfo)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onPeerFound = cb
}

func (m *MDNSService) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return fmt.Errorf("mDNS service is closed")
	}
	if m.started {
		return nil
	}

	m.discovery = mdns.NewMdnsService(m.host.Host, ServiceName, &mdnsNotifee{m: m})

	if err := m.discovery.Start(); err != nil {
		m.log(logger.Warning, fmt.Sprintf("mDNS start failed (non-critical): %v", err))
		m.discovery = nil
		return nil
	}

	m.started = true
	m.log(logger.Info, "mDNS service started")
	return nil
}

func (m *MDNSService) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil
	}
	m.closed = true

	if !m.started || m.discovery == nil {
		return nil
	}

	if err := m.discovery.Close(); err != nil {
		m.log(logger.Warning, fmt.Sprintf("mDNS close error: %v", err))
	}

	m.started = false
	m.discovery = nil
	m.log(logger.Info, "mDNS service stopped")
	return nil
}

func (m *MDNSService) HandlePeerFound(info peer.AddrInfo) {
	m.mu.RLock()
	started := m.started
	onPeerFound := m.onPeerFound
	m.mu.RUnlock()

	if !started {
		return
	}

	if info.ID == m.host.ID() || len(info.Addrs) == 0 {
		return
	}

	m.log(logger.Info, fmt.Sprintf("mDNS discovered peer: %s", info.ID.ShortString()))

	if onPeerFound != nil {
		go onPeerFound(info)
	}
}

type mdnsNotifee struct {
	m *MDNSService
}

func (n *mdnsNotifee) HandlePeerFound(info peer.AddrInfo) {
	n.m.HandlePeerFound(info)
}

func extractIPFromAddrInfo(info peer.AddrInfo) string {
	for _, addr := range info.Addrs {
		if ip, err := addr.ValueForProtocol(1); err == nil {
			return ip
		}
		if ip, err := addr.ValueForProtocol(2); err == nil {
			return ip
		}
	}
	return ""
}

func (m *MDNSService) TryConnect(ctx context.Context, info peer.AddrInfo) {
	m.mu.RLock()
	started := m.started
	m.mu.RUnlock()

	if !started || m.host == nil {
		return
	}

	m.log(logger.Debug, fmt.Sprintf("mDNS: connecting to %s", info.ID.ShortString()))

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := m.host.Connect(ctx, info); err != nil {
		m.log(logger.Debug, fmt.Sprintf("mDNS: connection to %s failed: %v", info.ID.ShortString(), err))
		return
	}

	m.log(logger.Info, fmt.Sprintf("mDNS: connected to %s", info.ID.ShortString()))
}

func (m *MDNSService) log(level logger.LogLevel, msg string) {
	if m.logr == nil {
		return
	}
	switch level {
	case logger.Trace:
		m.logr.Trace("mdns", msg)
	case logger.Debug:
		m.logr.Debug("mdns", msg)
	case logger.Info:
		m.logr.Info("mdns", msg)
	case logger.Warning:
		m.logr.Warn("mdns", msg)
	case logger.Error:
		m.logr.Error("mdns", msg)
	}
}

func (m *MDNSService) IsStarted() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.started && !m.closed
}
