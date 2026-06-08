package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"parade/internal/core/crypto"
	"parade/internal/core/db"
	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
	"parade/internal/network"
)

const IdentityFile = "./.parade_identity"

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return ""
}

type subscription struct {
	topic string
	id    eventbus.SubscriptionID
}

type App struct {
	ctx      context.Context
	evBus    eventbus.EventBus
	crypto   crypto.Engine
	database db.Database
	netEng   NetworkEngine
	fileEng  FileEngine
	ui       Frontend
	logr     logger.Logger

	isLoggedIn    bool
	subs          []subscription
	peerJoinedAt  map[string]time.Time
	convUpdatedAt map[string]time.Time
	peerJoinedMu  sync.Mutex
}

func NewApp(bus eventbus.EventBus, cry crypto.Engine, d db.Database, net NetworkEngine, file FileEngine, ui Frontend, logr logger.Logger) *App {
	return &App{
		evBus:         bus,
		crypto:        cry,
		database:      d,
		netEng:        net,
		fileEng:       file,
		ui:            ui,
		logr:          logr,
		peerJoinedAt:  make(map[string]time.Time),
		convUpdatedAt: make(map[string]time.Time),
	}
}

func (a *App) log(level logger.LogLevel, source, msg string) {
	if a.logr != nil {
		switch level {
		case logger.Trace:
			a.logr.Trace(source, msg)
		case logger.Debug:
			a.logr.Debug(source, msg)
		case logger.Info:
			a.logr.Info(source, msg)
		case logger.Warning:
			a.logr.Warn(source, msg)
		case logger.Error:
			a.logr.Error(source, msg)
		}
	}
}

// Startup 在 Wails 启动时调用
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	if w, ok := a.ui.(interface{ SetContext(context.Context) }); ok {
		w.SetContext(ctx)
	}
	if broker, ok := a.logr.(*logger.LogBroker); ok {
		broker.SetCallback(func(entry logger.LogEntry) {
			a.ui.Notify("ui_log", map[string]interface{}{
				"time":    entry.Timestamp.Format("15:04:05.000"),
				"level":   int(entry.Level),
				"source":  entry.Source,
				"message": entry.Message,
			})
		})
	}
	a.registerEventSubscribers()
	a.log(logger.Info, "system", "app started")
}

// GetContext returns the stored Wails context, used for single-instance window activation.
func (a *App) GetContext() context.Context {
	return a.ctx
}

// Shutdown 清理 EventBus 订阅，防止内存泄漏
func (a *App) Shutdown() {
	a.log(logger.Info, "system", "app shutting down")
	if a.netEng != nil {
		_ = a.netEng.SavePeers()
	}
	if a.crypto != nil {
		_ = a.crypto.SaveTeamKeys()
	}
	for _, s := range a.subs {
		a.evBus.Unsubscribe(s.topic, s.id)
	}
	a.subs = nil
}

func (a *App) subscribe(topic string, handler eventbus.EventHandler) {
	id := a.evBus.Subscribe(topic, handler)
	a.subs = append(a.subs, subscription{topic: topic, id: id})
}

func (a *App) requireFileEngine() error {
	if a.fileEng == nil {
		return errors.New("file engine not available")
	}
	return nil
}

func (a *App) registerEventSubscribers() {
	a.subscribe(eventbus.TopicPeerJoined, func(c context.Context, ev eventbus.Event) {
		if payload, ok := ev.Payload.(eventbus.PeerEventPayload); ok {
			a.peerJoinedMu.Lock()
			if last, ok := a.peerJoinedAt[payload.PeerUUID]; ok && time.Since(last) < 5*time.Second {
				a.peerJoinedMu.Unlock()
				return
			}
			a.peerJoinedAt[payload.PeerUUID] = time.Now()
			a.peerJoinedMu.Unlock()

			a.log(logger.Info, "eventbus", fmt.Sprintf("peer joined: %s", payload.PeerUUID))
			go func() {
				a.StartPrivateConversation(payload.PeerUUID)
				a.syncAllConversationsWithPeer(payload.PeerUUID)
			}()
		}
		a.ui.Notify("ui_peer_joined", ev.Payload)
	})

	a.subscribe(eventbus.TopicPeerLeft, func(c context.Context, ev eventbus.Event) {
		if payload, ok := ev.Payload.(eventbus.PeerEventPayload); ok {
			a.log(logger.Info, "eventbus", fmt.Sprintf("peer left: %s", payload.PeerUUID))
		}
		a.ui.Notify("ui_peer_left", ev.Payload)
	})

	a.subscribe(eventbus.TopicMsgReceived, func(c context.Context, ev eventbus.Event) {
		payload, ok := ev.Payload.(eventbus.MsgReceivedPayload)
		if !ok {
			return
		}

		if payload.SenderID == a.crypto.GetPersonalUUID() {
			return
		}

		a.log(logger.Debug, "eventbus", fmt.Sprintf("msg received from %s type=%d len=%d", payload.SenderID, payload.Type, len(payload.Content)))

		// Type 99 = ephemeral test message, skip DB persistence
		if payload.Type == 99 {
			a.ui.Notify("ui_new_message", map[string]interface{}{
				"id":        "test-" + payload.HLC,
				"hlc":       payload.HLC,
				"sender":    payload.SenderID,
				"content":   "[握手测试] " + string(payload.Content),
				"timestamp": ev.Timestamp,
			})
			return
		}

		encrypted, err := a.crypto.EncryptTeam(payload.Content)
		if err != nil {
			a.log(logger.Warning, "app", fmt.Sprintf("encrypt team failed: %v", err))
			return
		}
		msg := &db.Message{
			ID:             uuid.New().String(),
			HLC:            payload.HLC,
			SenderID:       payload.SenderID,
			ReceiverID:     payload.ReceiverID,
			TeamID:         payload.TeamID,
			ChannelID:      payload.ChannelID,
			ConversationID: payload.ConversationID,
			Content:        encrypted,
			Type:           payload.Type,
			CreatedAt:      ev.Timestamp,
		}
		if err := a.database.InsertMessage(a.ctx, msg); err != nil {
			a.log(logger.Error, "app", fmt.Sprintf("insert message failed: %v", err))
		}
		_ = a.database.UpsertConversation(a.ctx, &db.Conversation{
			ID: payload.ConversationID, TeamID: payload.TeamID, Type: "team",
			UpdatedAt: ev.Timestamp,
		})
		_ = a.database.UpdateConversationLastHLC(a.ctx, payload.ConversationID, payload.HLC)

		a.ui.Notify("ui_new_message", map[string]interface{}{
			"id":              msg.ID,
			"hlc":             msg.HLC,
			"sender":          msg.SenderID,
			"team_id":         msg.TeamID,
			"conversation_id": msg.ConversationID,
			"content":         string(payload.Content),
			"timestamp":       msg.CreatedAt,
		})
	})

	a.subscribe(eventbus.TopicPrivateMsgReceived, func(c context.Context, ev eventbus.Event) {
		payload, ok := ev.Payload.(eventbus.MsgReceivedPayload)
		if !ok {
			return
		}

		if payload.SenderID == a.crypto.GetPersonalUUID() {
			return
		}

		a.log(logger.Debug, "eventbus", fmt.Sprintf("private msg received from %s type=%d len=%d", payload.SenderID, payload.Type, len(payload.Content)))

		senderPubkey, err := a.netEng.ResolveUUID(payload.SenderID)
		if err != nil {
			a.log(logger.Warning, "app", fmt.Sprintf("resolve UUID failed: %v", err))
			return
		}
		encrypted, err := a.crypto.EncryptPrivate(payload.Content, senderPubkey)
		if err != nil {
			a.log(logger.Warning, "app", fmt.Sprintf("encrypt private failed: %v", err))
			return
		}
		convID := DerivePrivateConvID(a.crypto.GetPersonalUUID(), payload.SenderID)
		msg := &db.Message{
			ID:             uuid.New().String(),
			HLC:            payload.HLC,
			SenderID:       payload.SenderID,
			ReceiverID:     payload.ReceiverID,
			TeamID:         payload.TeamID,
			ConversationID: convID,
			Content:        encrypted,
			Type:           payload.Type,
			CreatedAt:      ev.Timestamp,
		}
		if err := a.database.InsertMessage(a.ctx, msg); err != nil {
			a.log(logger.Error, "app", fmt.Sprintf("insert private message failed: %v", err))
		}
		a.ensureConversation(convID, "private", payload.SenderID, senderPubkey)
		_ = a.database.UpdateConversationLastHLC(a.ctx, convID, payload.HLC)

		a.ui.Notify("ui_new_message", map[string]interface{}{
			"id":              msg.ID,
			"hlc":             msg.HLC,
			"sender":          msg.SenderID,
			"team_id":         msg.TeamID,
			"conversation_id": convID,
			"content":         string(payload.Content),
			"timestamp":       msg.CreatedAt,
		})
	})

	a.subscribe(eventbus.TopicFileProgress, func(c context.Context, ev eventbus.Event) {
		if payload, ok := ev.Payload.(eventbus.FileProgressPayload); ok {
			a.log(logger.Debug, "eventbus", fmt.Sprintf("file progress: task=%s %d/%d", payload.TaskID, payload.Transferred, payload.TotalSize))
		}
		a.ui.Notify("ui_file_progress", ev.Payload)
	})

	a.subscribe(eventbus.TopicFileCompleted, func(c context.Context, ev eventbus.Event) {
		if payload, ok := ev.Payload.(string); ok {
			a.log(logger.Info, "eventbus", fmt.Sprintf("file completed: %s", payload))
		}
		a.ui.Notify("ui_file_completed", ev.Payload)
	})

	a.subscribe(eventbus.TopicPeerOnline, func(c context.Context, ev eventbus.Event) {
		payload, ok := ev.Payload.(eventbus.PeerEventPayload)
		if !ok {
			return
		}
		a.ui.Notify("ui_peer_status", map[string]interface{}{
			"uuid":   payload.PeerUUID,
			"status": "online",
		})
		go func() {
			a.syncAllConversationsWithPeer(payload.PeerUUID)
		}()
	})

	a.subscribe(eventbus.TopicPeerOffline, func(c context.Context, ev eventbus.Event) {
		payload, ok := ev.Payload.(eventbus.PeerEventPayload)
		if !ok {
			return
		}
		a.ui.Notify("ui_peer_status", map[string]interface{}{
			"uuid":   payload.PeerUUID,
			"status": "offline",
		})
	})

	a.subscribe(eventbus.TopicConvSyncRequest, func(c context.Context, ev eventbus.Event) {
		payload, ok := ev.Payload.(eventbus.ConversationSyncPayload)
		if !ok {
			return
		}
		if payload.Messages != nil {
			var incoming []*db.Message
			if err := json.Unmarshal(payload.Messages, &incoming); err != nil {
				a.log(logger.Warning, "eventbus", fmt.Sprintf("conv sync response unmarshal failed: %v", err))
				return
			}
			if len(incoming) > 0 {
				_ = a.database.RunInTx(a.ctx, func(tx db.DBTx) error {
					for _, m := range incoming {
						_ = tx.InsertMessageTx(a.ctx, m)
					}
					return nil
				})
				_ = a.database.UpdateConversationLastHLC(a.ctx, payload.ConversationID, incoming[len(incoming)-1].HLC)
				a.peerJoinedMu.Lock()
				if last, ok := a.convUpdatedAt[payload.ConversationID]; !ok || time.Since(last) > 3*time.Second {
					a.convUpdatedAt[payload.ConversationID] = time.Now()
					a.peerJoinedMu.Unlock()
					a.ui.Notify("ui_conversation_updated", nil)
				} else {
					a.peerJoinedMu.Unlock()
				}
			}
			return
		}
		msgs, err := a.database.GetConversationMessagesSinceHLC(a.ctx, payload.ConversationID, payload.SinceHLC, 500)
		if err != nil || len(msgs) == 0 {
			a.log(logger.Debug, "sync", fmt.Sprintf("sync request from %s conv=%s: no messages since %s", truncate(payload.RequesterUUID, 16), payload.ConversationID[:8], payload.SinceHLC))
			return
		}
		a.log(logger.Debug, "sync", fmt.Sprintf("sync request from %s conv=%s: sending %d messages", truncate(payload.RequesterUUID, 16), payload.ConversationID[:8], len(msgs)))
		msgData, _ := json.Marshal(msgs)
		if a.netEng != nil {
			_ = a.netEng.SendConvSyncResponse(payload.RequesterUUID, payload.ConversationID, msgData)
		}
	})
}

// ---- 前端 API ----

func (a *App) Register(password string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("Register called (%d chars)", len(password)))
	err := a.crypto.RegisterIdentity(password, IdentityFile)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("Register failed: %v", err))
	}
	return err
}

func (a *App) Login(password string) error {
	a.log(logger.Debug, "ipc", "Login called")
	if err := a.crypto.LoadIdentity(password, IdentityFile); err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("Login failed: %v", err))
		return err
	}
	a.isLoggedIn = true
	for _, w := range a.crypto.IdentityLoadWarnings() {
		a.log(logger.Warning, "ipc", fmt.Sprintf("identity warning: %v", w))
	}
	if len(a.crypto.GetTeamIDs()) > 0 && a.netEng != nil {
		if serr := a.netEng.Start(4327); serr != nil {
			a.log(logger.Warning, "ipc", fmt.Sprintf("auto-start network failed: %v", serr))
		}
	}
	return nil
}

func (a *App) CheckHasIdentity() bool {
	a.log(logger.Debug, "ipc", "CheckHasIdentity called")
	_, err := os.Stat(IdentityFile)
	return !os.IsNotExist(err)
}

func (a *App) JoinTeam(secret string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("JoinTeam called (%d chars)", len(secret)))
	if err := a.checkLoggedIn(); err != nil { return err }
	_, err := a.JoinTeamWithName("", secret)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("JoinTeam failed: %v", err))
	}
	return err
}

func (a *App) JoinTeamWithName(name, secret string) (string, error) {
	a.log(logger.Debug, "ipc", fmt.Sprintf("JoinTeamWithName called (name=%s, %d chars)", name, len(secret)))
	if err := a.checkLoggedIn(); err != nil { return "", err }

	// Derive deterministic team UUID from secret (same secret → same UUID across devices)
	teamKey := sha256.Sum256([]byte(secret))
	teamID := deriveUUID(teamNS, teamKey[:])
	teamHash := fmt.Sprintf("%x", sha256.Sum256(teamKey[:]))

	if name == "" {
		existing, _ := a.database.GetTeamByHash(a.ctx, teamHash)
		if existing != nil {
			name = existing.Name
		} else {
			name = "Default Team"
		}
	}

	a.crypto.SetTeamKeyForTeam(teamID, secret)
	if err := a.crypto.SetActiveTeam(teamID); err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("JoinTeamWithName failed: %v", err))
		return "", err
	}

	team := &db.Team{
		ID:        teamID,
		Name:      name,
		TeamHash:  teamHash,
		CreatedAt: time.Now(),
	}
	if err := a.database.InsertTeam(a.ctx, team); err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("JoinTeamWithName failed: %v", err))
		return "", fmt.Errorf("failed to persist team: %w", err)
	}

	if a.netEng != nil {
		if err := a.netEng.Start(4327); err != nil {
			a.log(logger.Warning, "ipc", fmt.Sprintf("JoinTeamWithName failed: %v", err))
			return "", err
		}
	}
	convID := DeriveTeamConvID(teamID)
	a.ensureConversation(convID, "team", "", "")
	return teamID, nil
}

func (a *App) LeaveTeam(teamID string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("LeaveTeam called (team=%s)", teamID))
	if err := a.checkLoggedIn(); err != nil { return err }
	if err := a.database.DeleteTeam(a.ctx, teamID); err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("LeaveTeam failed: %v", err))
		return fmt.Errorf("failed to delete team: %w", err)
	}
	a.crypto.RemoveTeamKey(teamID)
	return nil
}

func (a *App) SwitchTeam(teamID string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("SwitchTeam called (team=%s)", teamID))
	if err := a.checkLoggedIn(); err != nil { return err }
	err := a.crypto.SetActiveTeam(teamID)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("SwitchTeam failed: %v", err))
	}
	return err
}

func (a *App) ListTeams() ([]map[string]interface{}, error) {
	a.log(logger.Debug, "ipc", "ListTeams called")
	teams, err := a.database.ListTeams(a.ctx)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("ListTeams failed: %v", err))
		return nil, err
	}
	activeTeamID := a.crypto.GetActiveTeam()
	res := make([]map[string]interface{}, 0, len(teams))
	for _, t := range teams {
		entry := map[string]interface{}{
			"id":         t.ID,
			"name":       t.Name,
			"team_hash":  t.TeamHash,
			"created_at": t.CreatedAt,
			"active":     t.ID == activeTeamID,
		}
		res = append(res, entry)
	}
	return res, nil
}

func (a *App) GetActiveTeam() (string, error) {
	if err := a.checkLoggedIn(); err != nil { return "", err }
	return a.crypto.GetActiveTeam(), nil
}

func (a *App) GetPubKey() (string, error) {
	if err := a.checkLoggedIn(); err != nil { return "", err }
	return a.crypto.GetPublicKeyBase64(), nil
}

func (a *App) ListConversations() ([]map[string]interface{}, error) {
	if err := a.checkLoggedIn(); err != nil { return nil, err }
	a.log(logger.Debug, "ipc", "ListConversations called")
	convs, err := a.database.ListConversations(a.ctx, a.crypto.GetActiveTeam())
	if err != nil {
		return nil, err
	}
	res := make([]map[string]interface{}, 0, len(convs))
	for _, cv := range convs {
		res = append(res, map[string]interface{}{
			"id":           cv.ID,
			"team_id":      cv.TeamID,
			"type":         cv.Type,
			"display_name": cv.DisplayName,
			"peer_pubkey":  cv.PeerPubkey,
			"my_pubkey":    cv.MyPubkey,
			"last_hlc":     cv.LastHLC,
			"last_message": cv.LastMessage,
			"last_msg_time": cv.LastMsgTime.Format(time.RFC3339),
			"created_at":   cv.CreatedAt.Format(time.RFC3339),
			"updated_at":   cv.UpdatedAt.Format(time.RFC3339),
		})
	}
	return res, nil
}

func (a *App) GetConversationMessages(convID string, limit int, offset int) ([]map[string]interface{}, error) {
	if err := a.checkLoggedIn(); err != nil { return nil, err }
	a.log(logger.Debug, "ipc", fmt.Sprintf("GetConversationMessages called (conv=%s)", convID))
	conv, _ := a.database.GetConversation(a.ctx, convID)
	msgs, err := a.database.GetConversationMessages(a.ctx, convID, limit, offset)
	if err != nil {
		return nil, err
	}
	res := make([]map[string]interface{}, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		content := "[message corrupted]"
		if conv != nil && conv.Type == "private" {
			if conv.PeerCryptoKey != "" {
				dec, decErr := a.crypto.DecryptPrivate(m.Content, conv.PeerCryptoKey)
				if decErr == nil {
					content = string(dec)
				}
			} else if conv.PeerPubkey != "" {
				// Fallback: resolve from peerMap (for conversations created before migration)
				peerPubkey, resolveErr := a.netEng.ResolveUUID(conv.PeerPubkey)
				if resolveErr == nil {
					dec, decErr := a.crypto.DecryptPrivate(m.Content, peerPubkey)
					if decErr == nil {
						content = string(dec)
					}
				}
			}
		} else {
			dec, decErr := a.crypto.DecryptTeam(m.Content)
			if decErr == nil {
				content = string(dec)
			}
		}
		res = append(res, map[string]interface{}{
			"id": m.ID, "hlc": m.HLC, "sender": m.SenderID, "content": content,
			"conversation_id": m.ConversationID, "timestamp": m.CreatedAt,
		})
	}
	return res, nil
}

func (a *App) StartPrivateConversation(peerUUID string) (string, error) {
	if err := a.checkLoggedIn(); err != nil { return "", err }
	a.log(logger.Debug, "ipc", fmt.Sprintf("StartPrivateConversation called (peer=%s)", truncate(peerUUID, 16)))
	myUUID := a.crypto.GetPersonalUUID()
	convID := DerivePrivateConvID(myUUID, peerUUID)
	existing, _ := a.database.GetConversation(a.ctx, convID)
	pubkey, resolveErr := a.netEng.ResolveUUID(peerUUID)
	if resolveErr != nil {
		a.log(logger.Warning, "app", fmt.Sprintf("StartPrivateConversation: resolve UUID failed: %v", resolveErr))
	}
	conv := &db.Conversation{
		ID:            convID,
		TeamID:        a.crypto.GetActiveTeam(),
		Type:          "private",
		DisplayName:   truncate(peerUUID, 16),
		PeerPubkey:    peerUUID,
		MyPubkey:      myUUID,
		PeerCryptoKey: pubkey,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := a.database.UpsertConversation(a.ctx, conv); err != nil {
		return "", err
	}
	if existing == nil {
		a.ui.Notify("ui_conversation_updated", nil)
	}
	return convID, nil
}

func (a *App) GetPeersWithStatus() ([]map[string]interface{}, error) {
	if err := a.checkLoggedIn(); err != nil { return nil, err }
	if a.netEng == nil { return nil, nil }
	statuses := a.netEng.PeersWithStatus()
	res := make([]map[string]interface{}, 0, len(statuses))
	for _, s := range statuses {
		res = append(res, map[string]interface{}{
			"pubkey":         s.PeerUUID,
			"ip":             s.IPAddress,
			"status":         s.Status,
			"last_heartbeat": s.LastHeartbeat,
			"last_online":    s.LastOnlineAt,
		})
	}
	return res, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (a *App) checkLoggedIn() error {
	if !a.isLoggedIn {
		return errors.New("not logged in")
	}
	return nil
}

func (a *App) ensureConversation(convID, convType, peerUUID, peerCryptoKey string) {
	conv := &db.Conversation{
		ID:            convID,
		TeamID:        a.crypto.GetActiveTeam(),
		Type:          convType,
		DisplayName:   truncate(peerUUID, 16),
		PeerPubkey:    peerUUID,
		MyPubkey:      a.crypto.GetPersonalUUID(),
		PeerCryptoKey: peerCryptoKey,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	_ = a.database.UpsertConversation(a.ctx, conv)
}

func (a *App) syncAllConversationsWithPeer(peerUUID string) {
	if a.netEng == nil || a.database == nil {
		return
	}
	a.log(logger.Debug, "sync", fmt.Sprintf("syncAllConversationsWithPeer: start for %s", truncate(peerUUID, 16)))
	convs, err := a.database.ListAllConversations(a.ctx)
	if err != nil {
		a.log(logger.Warning, "eventbus", fmt.Sprintf("list all conversations failed: %v", err))
		return
	}
	a.log(logger.Debug, "sync", fmt.Sprintf("syncAllConversationsWithPeer: %d conversations to sync to %s", len(convs), truncate(peerUUID, 16)))
	for _, cv := range convs {
		_ = a.netEng.SendConvSyncRequest(peerUUID, cv.ID, "")
	}
}

func (a *App) sendConversationMessage(convID, text string, encryptFn func([]byte) ([]byte, error), sendFn func([]byte) error) {
	myUUID := a.crypto.GetPersonalUUID()
	hlc := GenerateHLC(myUUID)
	raw := []byte(text)
	teamID := a.crypto.GetActiveTeam()

	enc, err := encryptFn(raw)
	if err != nil {
		a.log(logger.Warning, "app", "encrypt for storage failed: "+err.Error())
		return
	}

	msgID := uuid.New().String()
	now := time.Now()
	msg := &db.Message{
		ID:             msgID,
		HLC:            hlc,
		SenderID:       myUUID,
		Content:        enc,
		TeamID:         teamID,
		ConversationID: convID,
		CreatedAt:      now,
	}
	if err := a.database.InsertMessage(a.ctx, msg); err != nil {
		a.log(logger.Error, "app", fmt.Sprintf("insert message failed: %v", err))
	}
	_ = a.database.UpdateConversationLastHLC(a.ctx, convID, hlc)

	a.ui.Notify("ui_new_message", map[string]interface{}{
		"id":              msgID,
		"hlc":             hlc,
		"sender":          myUUID,
		"team_id":         teamID,
		"conversation_id": convID,
		"content":         text,
		"timestamp":       now,
	})

	netPayload, _ := json.Marshal(eventbus.MsgReceivedPayload{
		HLC:            hlc,
		SenderID:       myUUID,
		Content:        raw,
		Type:           0,
		TeamID:         teamID,
		ConversationID: convID,
		SenderIP:       getLocalIP(),
		SenderPubKey:   a.crypto.GetPublicKeyBase64(),
	})
	encrypted, err := encryptFn(netPayload)
	if err != nil {
		a.log(logger.Warning, "app", "encrypt for send failed: "+err.Error())
		return
	}
	if err := sendFn(encrypted); err != nil {
		a.log(logger.Warning, "app", "send failed: "+err.Error())
	}
}

func (a *App) SendTeamChat(text string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("SendTeamChat called (%d chars)", len(text)))
	if err := a.checkLoggedIn(); err != nil { return err }
	convID := DeriveTeamConvID(a.crypto.GetActiveTeam())
	a.ensureConversation(convID, "team", "", "")
	a.sendConversationMessage(convID, text, a.crypto.EncryptTeam, a.netEng.BroadcastTeam)
	return nil
}

func (a *App) SendPrivateChat(targetUUID, text string) error {
	if err := a.checkLoggedIn(); err != nil { return err }
	a.log(logger.Debug, "ipc", fmt.Sprintf("SendPrivateChat called (to=%s, %d chars)", truncate(targetUUID, 16), len(text)))
	if targetUUID == "" {
		return errors.New("target UUID is required")
	}
	pubkey, err := a.netEng.ResolveUUID(targetUUID)
	if err != nil {
		return fmt.Errorf("resolve target UUID: %w", err)
	}
	convID := DerivePrivateConvID(a.crypto.GetPersonalUUID(), targetUUID)
	a.ensureConversation(convID, "private", targetUUID, pubkey)
	a.sendConversationMessage(convID, text,
		func(payload []byte) ([]byte, error) { return a.crypto.EncryptPrivate(payload, pubkey) },
		func(payload []byte) error { return a.netEng.UnicastPrivate(targetUUID, payload) },
	)
	return nil
}

func (a *App) GetPeers() ([]map[string]string, error) {
	if err := a.checkLoggedIn(); err != nil { return nil, err }
	a.log(logger.Debug, "ipc", "GetPeers called")
	return a.netEng.Peers(), nil
}

func (a *App) ShareDirectory(path string) error {
	if err := a.checkLoggedIn(); err != nil { return err }
	a.log(logger.Debug, "ipc", fmt.Sprintf("ShareDirectory called (path=%s)", path))
	if err := a.requireFileEngine(); err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("ShareDirectory failed: %v", err))
		return err
	}
	err := a.fileEng.ShareDirectory(path)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("ShareDirectory failed: %v", err))
	}
	return err
}

func (a *App) UnshareDirectory(path string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("UnshareDirectory called (path=%s)", path))
	if err := a.requireFileEngine(); err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("UnshareDirectory failed: %v", err))
		return err
	}
	err := a.fileEng.UnshareDirectory(path)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("UnshareDirectory failed: %v", err))
	}
	return err
}

func (a *App) GetDirectoryChildren(path string) (interface{}, error) {
	a.log(logger.Debug, "ipc", fmt.Sprintf("GetDirectoryChildren called (path=%s)", path))
	if err := a.requireFileEngine(); err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("GetDirectoryChildren failed: %v", err))
		return nil, err
	}

	if path == "" {
		return nil, errors.New("path is empty")
	}

	cleanPath := filepath.Clean(path)
	sharedRoots := a.fileEng.GetSharedRoots()
	allowed := false
	for _, root := range sharedRoots {
		if strings.HasPrefix(cleanPath, root+string(os.PathSeparator)) || cleanPath == root {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("path %s is not within any shared directory", cleanPath)
	}

	result, err := a.fileEng.GetDirectoryChildren(cleanPath)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("GetDirectoryChildren failed: %v", err))
	}
	return result, err
}

// GetRemoteDirectoryChildren 浏览远程对等节点的共享目录
func (a *App) GetRemoteDirectoryChildren(targetUUID, path string) ([]map[string]interface{}, error) {
	a.log(logger.Debug, "ipc", fmt.Sprintf("GetRemoteDirectoryChildren called (to=%s, path=%s)", targetUUID, path))
	if a.netEng == nil {
		return nil, errors.New("network engine not available")
	}
	if targetUUID == "" {
		return nil, errors.New("target UUID is required")
	}

	// Clean the path to prevent traversal patterns (empty path = list shared roots).
	cleanPath := path
	if path != "" {
		cleanPath = filepath.Clean(path)
	}

	entries, err := a.netEng.BrowseRemoteDirectory(targetUUID, cleanPath)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("GetRemoteDirectoryChildren failed: %v", err))
		return nil, err
	}
	result := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		result = append(result, map[string]interface{}{
			"name":        e.GetName(),
			"path":        e.GetPath(),
			"isDirectory": e.GetIsDirectory(),
			"size":        e.GetSize(),
			"hash":        e.GetHash(),
		})
	}
	return result, nil
}

// ---- 共享组 API ----

func (a *App) CreateShareGroup(name string) (string, error) {
	a.log(logger.Debug, "ipc", fmt.Sprintf("CreateShareGroup called (name=%s)", name))
	activeTeam := a.crypto.GetActiveTeam()
	groupID := deriveUUID(shareGroupNS, []byte(activeTeam+name))
	myUUID := a.crypto.GetPersonalUUID()
	sg := &db.ShareGroup{
		ID:        groupID,
		TeamID:    activeTeam,
		Name:      name,
		CreatedBy: myUUID,
		CreatedAt: time.Now(),
	}
	if err := a.database.CreateShareGroup(a.ctx, sg); err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("CreateShareGroup failed: %v", err))
		return "", fmt.Errorf("failed to create share group: %w", err)
	}
	return groupID, nil
}

func (a *App) ListShareGroups() ([]map[string]interface{}, error) {
	a.log(logger.Debug, "ipc", "ListShareGroups called")
	activeTeam := a.crypto.GetActiveTeam()
	groups, err := a.database.ListShareGroupsByTeam(a.ctx, activeTeam)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("ListShareGroups failed: %v", err))
		return nil, err
	}
	res := make([]map[string]interface{}, 0, len(groups))
	for _, g := range groups {
		res = append(res, map[string]interface{}{
			"id":         g.ID,
			"team_id":    g.TeamID,
			"name":       g.Name,
			"created_by": g.CreatedBy,
			"created_at": g.CreatedAt,
		})
	}
	return res, nil
}

func (a *App) AddDirectoryToShareGroup(groupID, dirPath string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("AddDirectoryToShareGroup called (group=%s, path=%s)", groupID, dirPath))
	err := a.database.AddDirectoryToShareGroup(a.ctx, groupID, dirPath)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("AddDirectoryToShareGroup failed: %v", err))
	}
	return err
}

func (a *App) RemoveDirectoryFromShareGroup(groupID, dirPath string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("RemoveDirectoryFromShareGroup called (group=%s, path=%s)", groupID, dirPath))
	err := a.database.RemoveDirectoryFromShareGroup(a.ctx, groupID, dirPath)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("RemoveDirectoryFromShareGroup failed: %v", err))
	}
	return err
}

func (a *App) DeleteShareGroup(groupID string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("DeleteShareGroup called (group=%s)", groupID))
	err := a.database.DeleteShareGroup(a.ctx, groupID)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("DeleteShareGroup failed: %v", err))
	}
	return err
}

func (a *App) GetShareGroupDirs(groupID string) ([]map[string]interface{}, error) {
	a.log(logger.Debug, "ipc", fmt.Sprintf("GetShareGroupDirs called (group=%s)", groupID))
	dirs, err := a.database.ListShareGroupDirs(a.ctx, groupID)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("GetShareGroupDirs failed: %v", err))
		return nil, err
	}
	res := make([]map[string]interface{}, 0, len(dirs))
	for _, d := range dirs {
		res = append(res, map[string]interface{}{
			"group_id": d.GroupID,
			"dir_path": d.DirPath,
			"added_at": d.AddedAt,
		})
	}
	return res, nil
}

func (a *App) StartDownload(targetUUID, virtualPath, localSavePath string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("StartDownload called (to=%s, file=%s)", targetUUID, virtualPath))
	if a.netEng == nil {
		return errors.New("network engine not available")
	}
	err := a.netEng.StartDownload(targetUUID, virtualPath, localSavePath)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("StartDownload failed: %v", err))
	}
	return err
}

func (a *App) GetDefaultDownloadDir() (string, error) {
	if err := a.checkLoggedIn(); err != nil { return "", err }
	home, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir(), nil
	}
	dir := filepath.Join(home, "Downloads")
	_ = os.MkdirAll(dir, 0o755)
	return dir, nil
}

// ConnectToPeer 执行对指定 IP 的三阶段连接测试。
func (a *App) ConnectToPeer(ipAddress string) (map[string]interface{}, error) {
	a.log(logger.Debug, "ipc", fmt.Sprintf("ConnectToPeer called (ip=%s)", ipAddress))
	if a.netEng == nil {
		return nil, errors.New("network engine not available")
	}
	result, err := a.netEng.ConnectToPeer(ipAddress)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("ConnectToPeer failed: %v", err))
		return nil, err
	}
	return map[string]interface{}{
		"ip":         result.IP,
		"pubkey":     result.PubKey,
		"phase1":     mapPhaseResult(result.Phase1),
		"phase2":     mapPhaseResult(result.Phase2),
		"phase3Send": mapPhaseResult(result.Phase3Send),
		"phase3Recv": mapPhaseResult(result.Phase3Recv),
	}, nil
}

func (a *App) OnForeground() {
	a.log(logger.Debug, "ipc", "OnForeground called")
	if a.netEng != nil {
		a.netEng.OnForeground()
	}
}

func (a *App) ExportLogs() (map[string]interface{}, error) {
	broker, ok := a.logr.(*logger.LogBroker)
	if !ok {
		return nil, errors.New("logger not available")
	}
	entries := broker.Entries()
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal logs: %w", err)
	}
	return map[string]interface{}{
		"content": string(data),
		"count":   len(entries),
	}, nil
}

func (a *App) WriteLogFile(filePath, content string) error {
	return os.WriteFile(filePath, []byte(content), 0644)
}

func mapPhaseResult(r network.PhaseResult) map[string]interface{} {
	return map[string]interface{}{
		"success": r.Success,
		"label":   r.Label,
		"error":   r.Error,
	}
}
