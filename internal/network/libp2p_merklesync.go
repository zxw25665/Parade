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

	"parade/internal/core/db"
	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
	merkleSync "parade/internal/core/sync"
)

const protocolMerkleSync = "/parade/merklesync/1.0.0"

// merkleSyncMessage is the wire format for Merkle sync protocol messages.
type merkleSyncMessage struct {
	Type        string            `json:"type"`
	ConvID      string            `json:"conv_id"`
	Level       int               `json:"level,omitempty"`
	BucketPaths []string          `json:"bucket_paths,omitempty"`
	Buckets     []merkleSync.BucketInfo `json:"buckets,omitempty"`
	RootHash    string            `json:"root_hash,omitempty"`
	SinceHLC    string            `json:"since_hlc,omitempty"`
	Messages    json.RawMessage   `json:"messages,omitempty"`
}

// libp2pMerkleSync handles the Merkle sync protocol over libp2p streams.
type libp2pMerkleSync struct {
	host         host.Host
	bus          eventbus.EventBus
	logr         logger.Logger
	handler      merkleSync.MerkleSyncHandler
	onUUIDLookup func(pid peer.ID) string
}

// NewLibp2pMerkleSync creates a new libp2pMerkleSync and registers the stream handler.
func NewLibp2pMerkleSync(h host.Host, bus eventbus.EventBus, logr logger.Logger) *libp2pMerkleSync {
	ms := &libp2pMerkleSync{host: h, bus: bus, logr: logr}
	h.SetStreamHandler(protocolMerkleSync, ms.handleStream)
	return ms
}

func (ms *libp2pMerkleSync) log(level logger.LogLevel, source, msg string) {
	if ms.logr != nil {
		switch level {
		case logger.Trace:
			ms.logr.Trace(source, msg)
		case logger.Debug:
			ms.logr.Debug(source, msg)
		case logger.Info:
			ms.logr.Info(source, msg)
		case logger.Warning:
			ms.logr.Warn(source, msg)
		case logger.Error:
			ms.logr.Error(source, msg)
		}
	}
}

// handleStream is the server-side handler for incoming Merkle sync streams.
// It reads a request, dispatches to the handler, and writes back the response.
func (ms *libp2pMerkleSync) handleStream(stream network.Stream) {
	defer stream.Close()

	remotePeerID := stream.Conn().RemotePeer()
	data, err := io.ReadAll(stream)
	if err != nil {
		ms.log(logger.Warning, "libp2p-merklesync", fmt.Sprintf("read stream from %s: %v", remotePeerID.ShortString(), err))
		return
	}

	var req merkleSyncMessage
	if err := json.Unmarshal(data, &req); err != nil {
		ms.log(logger.Warning, "libp2p-merklesync", fmt.Sprintf("unmarshal from %s: %v", remotePeerID.ShortString(), err))
		return
	}

	ctx := context.Background()
	var resp merkleSyncMessage
	resp.ConvID = req.ConvID

	switch req.Type {
	case "merkle_root_request":
		rootHash, err := ms.handler.HandleMerkleRootRequest(ctx, req.ConvID)
		if err != nil {
			ms.log(logger.Warning, "libp2p-merklesync", fmt.Sprintf("root request conv=%s: %v", req.ConvID[:8], err))
			return
		}
		resp.Type = "merkle_root_response"
		resp.RootHash = fmt.Sprintf("%x", rootHash)

	case "bucket_compare_request":
		buckets, err := ms.handler.HandleBucketCompare(ctx, req.ConvID, req.Level, req.BucketPaths)
		if err != nil {
			ms.log(logger.Warning, "libp2p-merklesync", fmt.Sprintf("bucket compare conv=%s level=%d: %v", req.ConvID[:8], req.Level, err))
			return
		}
		resp.Type = "bucket_compare_response"
		resp.Buckets = buckets

	case "fetch_messages_request":
		messages, err := ms.handler.HandleFetchMessages(ctx, req.ConvID, req.BucketPaths[0], req.SinceHLC)
		if err != nil {
			ms.log(logger.Warning, "libp2p-merklesync", fmt.Sprintf("fetch messages conv=%s bucket=%s: %v", req.ConvID[:8], req.BucketPaths[0], err))
			return
		}
		msgData, _ := json.Marshal(messages)
		resp.Type = "fetch_messages_response"
		resp.Messages = msgData

	case "push_messages":
		var messages []*db.Message
		if err := json.Unmarshal(req.Messages, &messages); err != nil {
			ms.log(logger.Warning, "libp2p-merklesync", fmt.Sprintf("unmarshal push messages: %v", err))
			return
		}
		for _, msg := range messages {
			_ = msg
		}
		resp.Type = "push_messages_ack"
		resp.ConvID = req.ConvID

	default:
		ms.log(logger.Warning, "libp2p-merklesync", fmt.Sprintf("unknown type %q from %s", req.Type, remotePeerID.ShortString()))
		return
	}

	respData, err := json.Marshal(resp)
	if err != nil {
		ms.log(logger.Warning, "libp2p-merklesync", fmt.Sprintf("marshal response: %v", err))
		return
	}

	if _, err := stream.Write(respData); err != nil {
		ms.log(logger.Warning, "libp2p-merklesync", fmt.Sprintf("write response: %v", err))
	}
}

// sendRequest opens a stream, sends a request, and reads the response.
func (ms *libp2pMerkleSync) sendRequest(targetPeerID peer.ID, req *merkleSyncMessage) (*merkleSyncMessage, error) {
	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stream, err := ms.host.NewStream(ctx, targetPeerID, protocolMerkleSync)
	if err != nil {
		return nil, fmt.Errorf("new stream: %w", err)
	}
	defer stream.Close()

	if _, err := stream.Write(reqData); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	respData, err := io.ReadAll(stream)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp merkleSyncMessage
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &resp, nil
}

// SendMerkleRootRequest sends a Merkle root request and returns the remote root hash.
func (ms *libp2pMerkleSync) SendMerkleRootRequest(targetPeerID peer.ID, convID string) ([]byte, error) {
	req := &merkleSyncMessage{
		Type:   "merkle_root_request",
		ConvID: convID,
	}
	resp, err := ms.sendRequest(targetPeerID, req)
	if err != nil {
		return nil, err
	}
	if resp.RootHash == "" {
		return make([]byte, 32), nil
	}
	hash, err := hexDecode(resp.RootHash)
	if err != nil {
		return nil, fmt.Errorf("decode root hash: %w", err)
	}
	return hash, nil
}

// SendBucketCompareRequest sends a bucket comparison request and returns remote bucket info.
func (ms *libp2pMerkleSync) SendBucketCompareRequest(targetPeerID peer.ID, convID string, level int, paths []string) ([]merkleSync.BucketInfo, error) {
	req := &merkleSyncMessage{
		Type:        "bucket_compare_request",
		ConvID:      convID,
		Level:       level,
		BucketPaths: paths,
	}
	resp, err := ms.sendRequest(targetPeerID, req)
	if err != nil {
		return nil, err
	}
	return resp.Buckets, nil
}

// SendFetchMessagesRequest sends a fetch messages request and returns the messages.
func (ms *libp2pMerkleSync) SendFetchMessagesRequest(targetPeerID peer.ID, convID, bucketPath, sinceHLC string) ([]*db.Message, error) {
	req := &merkleSyncMessage{
		Type:        "fetch_messages_request",
		ConvID:      convID,
		BucketPaths: []string{bucketPath},
		SinceHLC:    sinceHLC,
	}
	resp, err := ms.sendRequest(targetPeerID, req)
	if err != nil {
		return nil, err
	}
	if resp.Messages == nil {
		return nil, nil
	}
	var messages []*db.Message
	if err := json.Unmarshal(resp.Messages, &messages); err != nil {
		return nil, fmt.Errorf("unmarshal messages: %w", err)
	}
	return messages, nil
}

// SendPushMessages sends messages to a remote peer for insertion.
func (ms *libp2pMerkleSync) SendPushMessages(targetPeerID peer.ID, convID string, messages []*db.Message) error {
	msgData, _ := json.Marshal(messages)
	req := &merkleSyncMessage{
		Type:     "push_messages",
		ConvID:   convID,
		Messages: msgData,
	}
	resp, err := ms.sendRequest(targetPeerID, req)
	if err != nil {
		return err
	}
	if resp.Type != "push_messages_ack" {
		return fmt.Errorf("unexpected response type: %s", resp.Type)
	}
	return nil
}

func hexDecode(s string) ([]byte, error) {
	if len(s) == 0 {
		return make([]byte, 32), nil
	}
	b := make([]byte, 32)
	_, err := fmt.Sscanf(s, "%x", &b)
	return b, err
}
