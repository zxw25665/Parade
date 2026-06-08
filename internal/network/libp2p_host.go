package network

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"sync"

	"github.com/libp2p/go-libp2p"
	libp2pCrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/muxer/yamux"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"

	"parade/internal/core/crypto"
	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
)

func deriveLibp2pKey(curvePriv []byte) (libp2pCrypto.PrivKey, error) {
	if len(curvePriv) == 0 {
		return nil, fmt.Errorf("deriveLibp2pKey: empty curve private key")
	}
	hash := sha256.Sum256(curvePriv)
	seed := hash[:]
	key := ed25519.NewKeyFromSeed(seed)
	priv, err := libp2pCrypto.UnmarshalEd25519PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("deriveLibp2pKey: unmarshal ed25519 private key: %w", err)
	}
	return priv, nil
}

func pubKeyToPeerID(curvePub []byte) (peer.ID, error) {
	if len(curvePub) == 0 {
		return "", fmt.Errorf("pubKeyToPeerID: empty curve public key")
	}
	hash := sha256.Sum256(curvePub)
	seed := hash[:]
	key := ed25519.NewKeyFromSeed(seed)
	pub := key.Public().(ed25519.PublicKey)
	lpPub, err := libp2pCrypto.UnmarshalEd25519PublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("pubKeyToPeerID: unmarshal ed25519 public key: %w", err)
	}
	pid, err := peer.IDFromPublicKey(lpPub)
	if err != nil {
		return "", fmt.Errorf("pubKeyToPeerID: derive peer ID: %w", err)
	}
	return pid, nil
}

type libp2pHost struct {
	host.Host
	bus    eventbus.EventBus
	crypto crypto.Engine
	logr   logger.Logger
	closeMu sync.Mutex
	closed  bool
}

func NewLibp2pHost(curvePriv []byte, port int, bus eventbus.EventBus, cry crypto.Engine, logr logger.Logger) (*libp2pHost, error) {
	priv, err := deriveLibp2pKey(curvePriv)
	if err != nil {
		return nil, fmt.Errorf("NewLibp2pHost: %w", err)
	}
	h, err := libp2p.New(
		libp2p.Identity(priv),
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port)),
		libp2p.Transport(tcp.NewTCPTransport),
		libp2p.Security(noise.ID, noise.New),
		libp2p.Muxer("/yamux/1.0.0", yamux.DefaultTransport),
	)
	if err != nil {
		return nil, fmt.Errorf("NewLibp2pHost: create host: %w", err)
	}
	return &libp2pHost{
		Host:   h,
		bus:    bus,
		crypto: cry,
		logr:   logr,
	}, nil
}

func (h *libp2pHost) Close() error {
	h.closeMu.Lock()
	defer h.closeMu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	if h.Host != nil {
		if err := h.Host.Close(); err != nil {
			return fmt.Errorf("Close: %w", err)
		}
	}
	return nil
}

func extractIP(pi peer.AddrInfo) string {
	for _, addr := range pi.Addrs {
		ip, err := addr.ValueForProtocol(multiaddr.P_IP4)
		if err == nil && ip != "" {
			return ip
		}
		ip, err = addr.ValueForProtocol(multiaddr.P_IP6)
		if err == nil && ip != "" {
			return ip
		}
	}
	return ""
}
