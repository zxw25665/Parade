package network

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	pubsub "github.com/libp2p/go-libp2p-pubsub"

	"parade/internal/core/crypto"
	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
)

const (
	protocolPrivateChat = "/parade/private-chat/1.0.0"
	topicPrefix         = "parade-chat-"
)

// privateMsg is the wire format for private chat stream messages.
type privateMsg struct {
	SenderUUID   string `json:"u"`
	SenderPubKey string `json:"k"`
	Payload      []byte `json:"p"`
}

// libp2pChat manages GossipSub pubsub for team chat and protocol streams for private messaging.
type libp2pChat struct {
	host            host.Host
	ps              *pubsub.PubSub
	topics          map[string]*pubsub.Topic
	subs            map[string]*pubsub.Subscription
	bus             eventbus.EventBus
	crypto          crypto.Engine
	logr            logger.Logger
	mu              sync.RWMutex
	onPubkeyLookup   func(uuid string) string
	onAutoRegister   func(pid peer.ID, uuid string)
	hasPeerIdentity  func(pid peer.ID) bool
	onPeerInfoReceived func(pid peer.ID, ip, pubkey string)
}

// NewLibp2pChat creates a new libp2pChat instance with GossipSub and registers the private chat stream handler.
func NewLibp2pChat(h host.Host, bus eventbus.EventBus, cry crypto.Engine, logr logger.Logger) (*libp2pChat, error) {
	ps, err := pubsub.NewGossipSub(context.Background(), h)
	if err != nil {
		return nil, fmt.Errorf("NewLibp2pChat: %w", err)
	}

	c := &libp2pChat{
		host:   h,
		ps:     ps,
		topics: make(map[string]*pubsub.Topic),
		subs:   make(map[string]*pubsub.Subscription),
		bus:    bus,
		crypto: cry,
		logr:   logr,
	}

	h.SetStreamHandler(protocolPrivateChat, c.handlePrivateStream)

	return c, nil
}

// topicName builds the GossipSub topic name for a team.
func topicName(teamHash string) string {
	return topicPrefix + teamHash
}

// JoinTeam subscribes to a team's GossipSub topic and starts consuming messages.
func (c *libp2pChat) JoinTeam(teamHash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	name := topicName(teamHash)

	if _, exists := c.topics[name]; exists {
		return nil
	}

	topic, err := c.ps.Join(name)
	if err != nil {
		return fmt.Errorf("JoinTeam %s: %w", teamHash, err)
	}

	sub, err := topic.Subscribe()
	if err != nil {
		topic.Close()
		return fmt.Errorf("JoinTeam %s subscribe: %w", teamHash, err)
	}

	c.topics[name] = topic
	c.subs[name] = sub

	go c.consumeMessages(sub)

	if c.logr != nil {
		c.logr.Info("libp2p-chat", fmt.Sprintf("joined team topic %s", name))
	}

	return nil
}

// LeaveTeam unsubscribes from a team's topic and removes it from tracking.
func (c *libp2pChat) LeaveTeam(teamHash string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	name := topicName(teamHash)

	if sub, ok := c.subs[name]; ok {
		sub.Cancel()
		delete(c.subs, name)
	}

	if topic, ok := c.topics[name]; ok {
		topic.Close()
		delete(c.topics, name)
	}

	if c.logr != nil {
		c.logr.Info("libp2p-chat", fmt.Sprintf("left team topic %s", name))
	}
}

// BroadcastTeam publishes a team message via GossipSub.
// The payload is already encrypted (EncryptTeam) by the caller.
func (c *libp2pChat) BroadcastTeam(teamHash string, payload []byte) error {
	c.mu.RLock()
	topic, ok := c.topics[topicName(teamHash)]
	c.mu.RUnlock()

	if !ok {
		return fmt.Errorf("BroadcastTeam: not joined team %s", teamHash)
	}

	if err := topic.Publish(context.Background(), payload); err != nil {
		return fmt.Errorf("BroadcastTeam: %w", err)
	}

	return nil
}

// UnicastPrivate sends a private message to a specific peer via a protocol stream.
// The payload is already encrypted (EncryptTeam wrapping EncryptPrivate) by the caller.
// senderUUID is the sender's Parade UUID, needed by the receiver for pubkey lookup.
func (c *libp2pChat) UnicastPrivate(targetPeerID peer.ID, senderUUID, senderPubKey string, payload []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := c.host.NewStream(ctx, targetPeerID, protocolPrivateChat)
	if err != nil {
		return fmt.Errorf("UnicastPrivate: %w", err)
	}
	defer stream.Close()

	msg := privateMsg{
		SenderUUID:   senderUUID,
		SenderPubKey: senderPubKey,
		Payload:      payload,
	}
	jsonBytes, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("UnicastPrivate marshal: %w", err)
	}

	// Write 4-byte length prefix + payload
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(jsonBytes)))
	if _, err := stream.Write(lenBuf); err != nil {
		return fmt.Errorf("UnicastPrivate write len: %w", err)
	}
	if _, err := stream.Write(jsonBytes); err != nil {
		return fmt.Errorf("UnicastPrivate write payload: %w", err)
	}

	// Read ACK
	ack := make([]byte, 1)
	if _, err := io.ReadFull(stream, ack); err != nil {
		return fmt.Errorf("UnicastPrivate read ack: %w", err)
	}

	return nil
}

// consumeMessages reads messages from a GossipSub subscription and publishes them on the EventBus.
func (c *libp2pChat) consumeMessages(sub *pubsub.Subscription) {
	ctx := context.Background()

	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			// Subscription canceled or context done
			return
		}

		// Skip messages from self
		if msg.ReceivedFrom == c.host.ID() {
			continue
		}

		// Decrypt team-encrypted payload
		plain, err := c.crypto.DecryptTeam(msg.Data)
		if err != nil {
			if c.logr != nil {
				c.logr.Warn("libp2p-chat", fmt.Sprintf("DecryptTeam failed: %v", err))
			}
			continue
		}

		// Unmarshal to MsgReceivedPayload
		var msgPayload eventbus.MsgReceivedPayload
		if err := json.Unmarshal(plain, &msgPayload); err != nil {
			if c.logr != nil {
				c.logr.Warn("libp2p-chat", fmt.Sprintf("unmarshal message failed: %v", err))
			}
			continue
		}

		// Auto-register the ORIGINAL publisher (not the relay peer)
		from := peer.ID(msg.From)
		if from != "" && c.hasPeerIdentity != nil && !c.hasPeerIdentity(from) {
			if c.onAutoRegister != nil {
				c.onAutoRegister(from, msgPayload.SenderID)
			}
		}
		// If the message carries sender's IP + pubkey, update the peer record
		if msgPayload.SenderIP != "" || msgPayload.SenderPubKey != "" {
			if c.onPeerInfoReceived != nil {
				c.onPeerInfoReceived(from, msgPayload.SenderIP, msgPayload.SenderPubKey)
			}
		}

		// Publish on EventBus — app layer handles persistence and UI
		c.bus.Publish(eventbus.TopicMsgReceived, msgPayload)
	}
}

// handlePrivateStream is the server-side handler for the private chat protocol stream.
func (c *libp2pChat) handlePrivateStream(stream network.Stream) {
	defer stream.Close()

	// Read 4-byte length prefix
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(stream, lenBuf); err != nil {
		if c.logr != nil {
			c.logr.Warn("libp2p-chat", fmt.Sprintf("private stream read len: %v", err))
		}
		return
	}

	payloadLen := binary.BigEndian.Uint32(lenBuf)
	if payloadLen > 256*1024 { // 256KB sanity limit
		if c.logr != nil {
			c.logr.Warn("libp2p-chat", "private stream payload too large")
		}
		return
	}

	// Read payload
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(stream, payload); err != nil {
		if c.logr != nil {
			c.logr.Warn("libp2p-chat", fmt.Sprintf("private stream read payload: %v", err))
		}
		return
	}

	// Unmarshal wire format
	var msg privateMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		if c.logr != nil {
			c.logr.Warn("libp2p-chat", fmt.Sprintf("private stream unmarshal: %v", err))
		}
		return
	}

	// Decrypt outer team layer
	teamPlain, err := c.crypto.DecryptTeam(msg.Payload)
	if err != nil {
		if c.logr != nil {
			c.logr.Warn("libp2p-chat", fmt.Sprintf("private DecryptTeam failed: %v", err))
		}
		return
	}

	// Pubkey: prefer from message directly, fallback to lookup
	senderPubKey := msg.SenderPubKey
	if senderPubKey == "" && c.onPubkeyLookup != nil {
		senderPubKey = c.onPubkeyLookup(msg.SenderUUID)
	}
	if senderPubKey == "" {
		if c.logr != nil {
			c.logr.Warn("libp2p-chat", fmt.Sprintf("private pubkey not available for UUID %s", msg.SenderUUID[:16]))
		}
		return
	}

	// Auto-register the peer on first contact
	if c.hasPeerIdentity != nil && !c.hasPeerIdentity(stream.Conn().RemotePeer()) {
		if c.onAutoRegister != nil {
			c.onAutoRegister(stream.Conn().RemotePeer(), msg.SenderUUID)
		}
	}

	// Decrypt inner private layer using sender's Curve25519 pubkey
	privatePlain, err := c.crypto.DecryptPrivate(teamPlain, senderPubKey)

	// Unmarshal to MsgReceivedPayload
	var msgPayload eventbus.MsgReceivedPayload
	if err := json.Unmarshal(privatePlain, &msgPayload); err != nil {
		if c.logr != nil {
			c.logr.Warn("libp2p-chat", fmt.Sprintf("private unmarshal message failed: %v", err))
		}
		return
	}

	msgPayload.ReceiverID = c.crypto.GetPersonalUUID()

	// Publish on EventBus
	c.bus.Publish(eventbus.TopicPrivateMsgReceived, msgPayload)

	// Send ACK
	stream.Write([]byte{0x01})
}

// Close shuts down all subscriptions. The GossipSub instance and host are closed separately.
func (c *libp2pChat) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, sub := range c.subs {
		sub.Cancel()
	}
	// Clear maps so LeaveTeam/JoinTeam don't interact with stale state
	c.subs = make(map[string]*pubsub.Subscription)
	c.topics = make(map[string]*pubsub.Topic)
}
