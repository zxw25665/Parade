package network

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"parade/internal/core/logger"
)

const (
	healthCheckInterval = 5 * time.Second
	initialBackoff      = 1 * time.Second
	maxBackoff          = 60 * time.Second
	backoffMultiplier   = 1.5
)

// PeerSession 管理到单个对等节点的连接生命周期，包括健康监测和自动重连。
type PeerSession struct {
	pubKey string
	ipAddr string
	engine *Engine

	mu          sync.Mutex
	controlConn *grpc.ClientConn
	dataConn    *grpc.ClientConn

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	reconnecting atomic.Bool

	logr logger.Logger
}

func NewPeerSession(pubKey, ipAddr string, eng *Engine, controlConn *grpc.ClientConn) *PeerSession {
	ctx, cancel := context.WithCancel(context.Background())
	return &PeerSession{
		pubKey:      pubKey,
		ipAddr:      ipAddr,
		engine:      eng,
		controlConn: controlConn,
		ctx:         ctx,
		cancel:      cancel,
	}
}

func (ps *PeerSession) WithLogger(l logger.Logger) *PeerSession {
	ps.logr = l
	return ps
}

func (ps *PeerSession) log(level logger.LogLevel, source, msg string) {
	if ps.logr != nil {
		switch level {
		case logger.Trace:
			ps.logr.Trace(source, msg)
		case logger.Debug:
			ps.logr.Debug(source, msg)
		case logger.Info:
			ps.logr.Info(source, msg)
		case logger.Warning:
			ps.logr.Warn(source, msg)
		case logger.Error:
			ps.logr.Error(source, msg)
		}
	}
}

func (ps *PeerSession) Start() {
	ps.wg.Add(1)
	go ps.healthLoop()
}

func (ps *PeerSession) Stop() {
	ps.cancel()
	ps.wg.Wait()
}

func (ps *PeerSession) GetControlConn() *grpc.ClientConn {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.controlConn
}

func (ps *PeerSession) GetDataConn() *grpc.ClientConn {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.dataConn
}

func (ps *PeerSession) healthLoop() {
	defer ps.wg.Done()

	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ps.ctx.Done():
			return
		case <-ticker.C:
			if ps.checkAndReconnect() {
				ps.engine.discovery.RefreshLastSeen(ps.pubKey)
			}
		}
	}
}

func (ps *PeerSession) checkAndReconnect() bool {
	ps.mu.Lock()
	conn := ps.controlConn
	ps.mu.Unlock()

	if conn == nil {
		return ps.reconnect()
	}

	state := conn.GetState()
	if state == connectivity.Ready || state == connectivity.Idle {
		return true
	}

	if state == connectivity.Connecting {
		return true
	}

	return ps.reconnect()
}

func (ps *PeerSession) reconnect() bool {
	if !ps.reconnecting.CompareAndSwap(false, true) {
		return false
	}
	defer ps.reconnecting.Store(false)

	backoff := initialBackoff
	for {
		select {
		case <-ps.ctx.Done():
			return false
		default:
		}

		newConn, err := ps.dialControl()
		if err != nil {
			ps.log(logger.Warning, "peer", fmt.Sprintf("reconnect to %s failed: %v, retrying in %v",
				truncateKey(ps.pubKey), err, backoff))
			select {
			case <-ps.ctx.Done():
				return false
			case <-time.After(backoff):
				backoff = time.Duration(float64(backoff) * backoffMultiplier)
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}

		ps.mu.Lock()
		oldConn := ps.controlConn
		ps.controlConn = newConn
		ps.mu.Unlock()

		ps.engine.connMu.Lock()
		ps.engine.clientConns[ps.pubKey] = newConn
		ps.engine.connMu.Unlock()

		if oldConn != nil {
			oldConn.Close()
		}

		ps.engine.dataConnMu.Lock()
		if oldDataConn, exists := ps.engine.dataClientConns[ps.pubKey]; exists {
			oldDataConn.Close()
			delete(ps.engine.dataClientConns, ps.pubKey)
		}
		ps.engine.dataConnMu.Unlock()

		ps.engine.discovery.UpsertPeer(PeerInfo{
			PubKeyBase64: ps.pubKey,
			IPAddress:    ps.ipAddr,
		})

		ps.log(logger.Info, "peer", fmt.Sprintf("reconnected to %s", truncateKey(ps.pubKey)))
		return true
	}
}

func (ps *PeerSession) dialControl() (*grpc.ClientConn, error) {
	target := fmt.Sprintf("%s:%d", ps.ipAddr, ps.engine.controlPort)
	return grpc.Dial(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                15 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
}
