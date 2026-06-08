package network

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
)

const protocolConvSync = "/parade/sync/1.0.0"

// syncMessage is the wire format for conversation sync protocol messages.
type syncMessage struct {
	Type     string          `json:"type"`     // "request" or "response"
	ConvID   string          `json:"conv_id"`
	SinceHLC string          `json:"since_hlc,omitempty"`
	Messages json.RawMessage `json:"messages,omitempty"`
}

// libp2pSync handles the conversation sync protocol stream.
type libp2pSync struct {
	host         host.Host
	bus          eventbus.EventBus
	logr         logger.Logger
	onUUIDLookup func(pid peer.ID) string
}

// NewLibp2pSync creates a new libp2pSync instance and registers the sync protocol stream handler.
func NewLibp2pSync(h host.Host, bus eventbus.EventBus, logr logger.Logger) *libp2pSync {
	s := &libp2pSync{host: h, bus: bus, logr: logr}
	h.SetStreamHandler(protocolConvSync, s.handleSync)
	return s
}

// handleSync is the server-side handler for the conversation sync protocol.
func (s *libp2pSync) handleSync(stream network.Stream) {
	defer stream.Close()

	remotePeerID := stream.Conn().RemotePeer()

	data, err := io.ReadAll(stream)
	if err != nil {
		if s.logr != nil {
			s.logr.Warn("libp2p-sync", fmt.Sprintf("read sync stream failed from %s: %v", remotePeerID.ShortString(), err))
		}
		return
	}

	var msg syncMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		if s.logr != nil {
			s.logr.Warn("libp2p-sync", fmt.Sprintf("unmarshal sync message from %s: %v", remotePeerID.ShortString(), err))
		}
		return
	}

	switch msg.Type {
	case "request":
		if s.logr != nil {
			s.logr.Debug("libp2p-sync", fmt.Sprintf("sync request from %s conv=%s since=%s", remotePeerID.ShortString(), msg.ConvID[:8], msg.SinceHLC))
		}
		requesterUUID := remotePeerID.String()
		if s.onUUIDLookup != nil {
			if uuid := s.onUUIDLookup(remotePeerID); uuid != "" {
				requesterUUID = uuid
			}
		}
		s.bus.Publish(eventbus.TopicConvSyncRequest, eventbus.ConversationSyncPayload{
			RequesterUUID:  requesterUUID,
			ConversationID: msg.ConvID,
			SinceHLC:       msg.SinceHLC,
			Messages:       nil,
		})

	case "response":
		if s.logr != nil {
			s.logr.Debug("libp2p-sync", fmt.Sprintf("sync response from %s conv=%s (%d bytes)", remotePeerID.ShortString(), msg.ConvID[:8], len(msg.Messages)))
		}
		requesterUUID := remotePeerID.String()
		if s.onUUIDLookup != nil {
			if uuid := s.onUUIDLookup(remotePeerID); uuid != "" {
				requesterUUID = uuid
			}
		}
		s.bus.Publish(eventbus.TopicConvSyncRequest, eventbus.ConversationSyncPayload{
			RequesterUUID:  requesterUUID,
			ConversationID: msg.ConvID,
			Messages:       msg.Messages,
		})

	default:
		if s.logr != nil {
			s.logr.Warn("libp2p-sync", fmt.Sprintf("unknown sync message type %q from %s", msg.Type, remotePeerID.ShortString()))
		}
	}
}

// SendConvSyncRequest sends a conversation sync request to a target peer.
func (s *libp2pSync) SendConvSyncRequest(targetPeerID peer.ID, convID, sinceHLC string) error {
	msg := syncMessage{
		Type:     "request",
		ConvID:   convID,
		SinceHLC: sinceHLC,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("SendConvSyncRequest marshal: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := s.host.NewStream(ctx, targetPeerID, protocolConvSync)
	if err != nil {
		return fmt.Errorf("SendConvSyncRequest new stream: %w", err)
	}
	defer stream.Close()

	if _, err := stream.Write(data); err != nil {
		return fmt.Errorf("SendConvSyncRequest write: %w", err)
	}

	return nil
}

// SendConvSyncResponse sends a conversation sync response with messages to a target peer.
func (s *libp2pSync) SendConvSyncResponse(targetPeerID peer.ID, convID string, messagesJSON []byte) error {
	msg := syncMessage{
		Type:     "response",
		ConvID:   convID,
		Messages: json.RawMessage(messagesJSON),
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("SendConvSyncResponse marshal: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := s.host.NewStream(ctx, targetPeerID, protocolConvSync)
	if err != nil {
		return fmt.Errorf("SendConvSyncResponse new stream: %w", err)
	}
	defer stream.Close()

	if _, err := stream.Write(data); err != nil {
		return fmt.Errorf("SendConvSyncResponse write: %w", err)
	}

	return nil
}
