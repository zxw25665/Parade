package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"parade/internal/core/crypto"
	"parade/internal/core/db"
	"parade/internal/core/eventbus"
	"parade/internal/network"
)

const IdentityFile = "./.parade_identity"

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

	isLoggedIn bool
	subs       []subscription
}

func NewApp(bus eventbus.EventBus, cry crypto.Engine, d db.Database, net NetworkEngine, file FileEngine, ui Frontend) *App {
	return &App{
		evBus:    bus,
		crypto:   cry,
		database: d,
		netEng:   net,
		fileEng:  file,
		ui:       ui,
	}
}

// Startup 在 Wails 启动时调用
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	if w, ok := a.ui.(interface{ SetContext(context.Context) }); ok {
		w.SetContext(ctx)
	}
	a.registerEventSubscribers()
}

// GetContext returns the stored Wails context, used for single-instance window activation.
func (a *App) GetContext() context.Context {
	return a.ctx
}

// Shutdown 清理 EventBus 订阅，防止内存泄漏
func (a *App) Shutdown() {
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
		a.ui.Notify("ui_peer_joined", ev.Payload)
	})

	a.subscribe(eventbus.TopicPeerLeft, func(c context.Context, ev eventbus.Event) {
		a.ui.Notify("ui_peer_left", ev.Payload)
	})

	a.subscribe(eventbus.TopicMsgReceived, func(c context.Context, ev eventbus.Event) {
		payload, ok := ev.Payload.(eventbus.MsgReceivedPayload)
		if !ok {
			return
		}

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

		encrypted, err := a.crypto.EncryptLocal(payload.Content)
		if err != nil {
			log.Printf("encrypt local failed: %v", err)
			return
		}
		msg := &db.Message{
			ID:         uuid.New().String(),
			HLC:        payload.HLC,
			SenderID:   payload.SenderID,
			ReceiverID: db.ReceiverIDGroupChat,
			TeamID:     payload.TeamID,
			ChannelID:  payload.ChannelID,
			Content:    encrypted,
			Type:       payload.Type,
			CreatedAt:  ev.Timestamp,
		}
		if err := a.database.InsertMessage(a.ctx, msg); err != nil {
			log.Printf("insert message failed: %v", err)
		}

		a.ui.Notify("ui_new_message", map[string]interface{}{
			"id":         msg.ID,
			"hlc":        msg.HLC,
			"sender":     msg.SenderID,
			"team_id":    msg.TeamID,
			"channel_id": msg.ChannelID,
			"content":    string(payload.Content),
			"timestamp":  msg.CreatedAt,
		})
	})

	a.subscribe(eventbus.TopicPrivateMsgReceived, func(c context.Context, ev eventbus.Event) {
		payload, ok := ev.Payload.(eventbus.MsgReceivedPayload)
		if !ok {
			return
		}

		encrypted, err := a.crypto.EncryptLocal(payload.Content)
		if err != nil {
			log.Printf("encrypt local (private) failed: %v", err)
			return
		}
		msg := &db.Message{
			ID:         uuid.New().String(),
			HLC:        payload.HLC,
			SenderID:   payload.SenderID,
			ReceiverID: payload.ReceiverID,
			TeamID:     payload.TeamID,
			ChannelID:  payload.ChannelID,
			Content:    encrypted,
			Type:       payload.Type,
			CreatedAt:  ev.Timestamp,
		}
		if err := a.database.InsertMessage(a.ctx, msg); err != nil {
			log.Printf("insert private message failed: %v", err)
		}

		a.ui.Notify("ui_private_message", map[string]interface{}{
			"id":         msg.ID,
			"hlc":        msg.HLC,
			"senderId":   msg.SenderID,
			"receiverId": msg.ReceiverID,
			"team_id":    msg.TeamID,
			"content":    string(payload.Content),
			"timestamp":  msg.CreatedAt,
		})
	})

	a.subscribe(eventbus.TopicFileProgress, func(c context.Context, ev eventbus.Event) {
		a.ui.Notify("ui_file_progress", ev.Payload)
	})

	a.subscribe(eventbus.TopicFileCompleted, func(c context.Context, ev eventbus.Event) {
		a.ui.Notify("ui_file_completed", ev.Payload)
	})
}

// ---- 前端 API ----

func (a *App) Register(password string) error {
	return a.crypto.RegisterIdentity(password, IdentityFile)
}

func (a *App) Login(password string) error {
	if err := a.crypto.LoadIdentity(password, IdentityFile); err != nil {
		return err
	}
	a.isLoggedIn = true
	return nil
}

func (a *App) CheckHasIdentity() bool {
	_, err := os.Stat(IdentityFile)
	return !os.IsNotExist(err)
}

func (a *App) JoinTeam(secret string) error {
	_, err := a.JoinTeamWithName("", secret)
	return err
}

func (a *App) JoinTeamWithName(name, secret string) (string, error) {
	teamID := uuid.New().String()
	if name == "" {
		name = "Default Team"
	}

	a.crypto.SetTeamKeyForTeam(teamID, secret)
	if err := a.crypto.SetActiveTeam(teamID); err != nil {
		return "", err
	}

	teamHash := a.crypto.TeamKeyHashFor(teamID)
	team := &db.Team{
		ID:        teamID,
		Name:      name,
		TeamHash:  teamHash,
		CreatedAt: time.Now(),
	}
	if err := a.database.InsertTeam(a.ctx, team); err != nil {
		return "", fmt.Errorf("failed to persist team: %w", err)
	}

	if a.netEng != nil {
		if err := a.netEng.Start(4327); err != nil {
			return "", err
		}
	}
	return teamID, nil
}

func (a *App) LeaveTeam(teamID string) error {
	if err := a.database.DeleteTeam(a.ctx, teamID); err != nil {
		return fmt.Errorf("failed to delete team: %w", err)
	}
	a.crypto.RemoveTeamKey(teamID)
	return nil
}

func (a *App) SwitchTeam(teamID string) error {
	return a.crypto.SetActiveTeam(teamID)
}

func (a *App) CreateChannel(name string) error {
	channelID := uuid.New().String()
	activeTeam := a.crypto.GetActiveTeam()
	myPub := a.crypto.GetPublicKeyBase64()
	ch := &db.Channel{
		ID:        channelID,
		TeamID:    activeTeam,
		Name:      name,
		CreatedBy: myPub,
		CreatedAt: time.Now(),
	}
	if err := a.database.CreateChannel(a.ctx, ch); err != nil {
		return fmt.Errorf("failed to create channel: %w", err)
	}
	return a.database.AddChannelMember(a.ctx, channelID, myPub)
}

func (a *App) ListChannels() ([]map[string]interface{}, error) {
	activeTeam := a.crypto.GetActiveTeam()
	channels, err := a.database.ListChannelsByTeam(a.ctx, activeTeam)
	if err != nil {
		return nil, err
	}
	res := make([]map[string]interface{}, 0, len(channels))
	for _, c := range channels {
		res = append(res, map[string]interface{}{
			"id":         c.ID,
			"team_id":    c.TeamID,
			"name":       c.Name,
			"created_by": c.CreatedBy,
			"created_at": c.CreatedAt,
		})
	}
	return res, nil
}

func (a *App) JoinChannel(channelID string) error {
	myPub := a.crypto.GetPublicKeyBase64()
	return a.database.AddChannelMember(a.ctx, channelID, myPub)
}

func (a *App) LeaveChannel(channelID string) error {
	myPub := a.crypto.GetPublicKeyBase64()
	return a.database.RemoveChannelMember(a.ctx, channelID, myPub)
}

func (a *App) ListTeams() ([]map[string]interface{}, error) {
	teams, err := a.database.ListTeams(a.ctx)
	if err != nil {
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

func (a *App) GetActiveTeam() string {
	return a.crypto.GetActiveTeam()
}

func (a *App) sendMessageWith(text string, receiverID string, channelID string, encryptFn func([]byte) ([]byte, error), sendFn func([]byte) error) error {
	myPub := a.crypto.GetPublicKeyBase64()
	hlc := GenerateHLC(myPub)
	raw := []byte(text)

	enc, err := a.crypto.EncryptLocal(raw)
	if err != nil {
		return err
	}
	if err := a.database.InsertMessage(a.ctx, &db.Message{
		ID: uuid.New().String(), HLC: hlc, SenderID: myPub, Content: enc, ReceiverID: receiverID, TeamID: a.crypto.GetActiveTeam(), ChannelID: channelID, CreatedAt: time.Now(),
	}); err != nil {
		log.Printf("insert message failed: %v", err)
	}

	netMsg := eventbus.MsgReceivedPayload{HLC: hlc, SenderID: myPub, Content: raw}
	jsonBytes, err := json.Marshal(netMsg)
	if err != nil {
		return err
	}
	encrypted, err := encryptFn(jsonBytes)
	if err != nil {
		return err
	}
	return sendFn(encrypted)
}

func (a *App) SendTeamChat(text string) error {
	return a.sendMessageWith(text, db.ReceiverIDGroupChat, "", a.crypto.EncryptTeam, a.netEng.BroadcastTeam)
}

func (a *App) SendPrivateChat(targetPubKey, text string) error {
	if targetPubKey == "" {
		return errors.New("target public key is required")
	}
	return a.sendMessageWith(text, targetPubKey, "",
		func(payload []byte) ([]byte, error) { return a.crypto.EncryptPrivate(payload, targetPubKey) },
		func(payload []byte) error { return a.netEng.UnicastPrivate(targetPubKey, payload) },
	)
}

func (a *App) SendChannelChat(channelID, text string) error {
	return a.sendMessageWith(text, db.ReceiverIDGroupChat, channelID, a.crypto.EncryptTeam, func(payload []byte) error {
		return a.netEng.BroadcastChannel(channelID, payload)
	})
}

func (a *App) GetPeers() []map[string]string {
	return a.netEng.Peers()
}

func (a *App) ShareDirectory(path string) error {
	if err := a.requireFileEngine(); err != nil {
		return err
	}
	return a.fileEng.ShareDirectory(path)
}

func (a *App) UnshareDirectory(path string) error {
	if err := a.requireFileEngine(); err != nil {
		return err
	}
	return a.fileEng.UnshareDirectory(path)
}

func (a *App) GetDirectoryChildren(path string) (interface{}, error) {
	if err := a.requireFileEngine(); err != nil {
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

	return a.fileEng.GetDirectoryChildren(cleanPath)
}

// GetRemoteDirectoryChildren 浏览远程对等节点的共享目录
func (a *App) GetRemoteDirectoryChildren(targetPubKey, path string) ([]map[string]interface{}, error) {
	if a.netEng == nil {
		return nil, errors.New("network engine not available")
	}
	if targetPubKey == "" {
		return nil, errors.New("target public key is required")
	}

	// Clean the path to prevent traversal patterns.
	cleanPath := filepath.Clean(path)

	entries, err := a.netEng.BrowseRemoteDirectory(targetPubKey, cleanPath)
	if err != nil {
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
	groupID := uuid.New().String()
	activeTeam := a.crypto.GetActiveTeam()
	myPub := a.crypto.GetPublicKeyBase64()
	sg := &db.ShareGroup{
		ID:        groupID,
		TeamID:    activeTeam,
		Name:      name,
		CreatedBy: myPub,
		CreatedAt: time.Now(),
	}
	if err := a.database.CreateShareGroup(a.ctx, sg); err != nil {
		return "", fmt.Errorf("failed to create share group: %w", err)
	}
	return groupID, nil
}

func (a *App) ListShareGroups() ([]map[string]interface{}, error) {
	activeTeam := a.crypto.GetActiveTeam()
	groups, err := a.database.ListShareGroupsByTeam(a.ctx, activeTeam)
	if err != nil {
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
	return a.database.AddDirectoryToShareGroup(a.ctx, groupID, dirPath)
}

func (a *App) RemoveDirectoryFromShareGroup(groupID, dirPath string) error {
	return a.database.RemoveDirectoryFromShareGroup(a.ctx, groupID, dirPath)
}

func (a *App) DeleteShareGroup(groupID string) error {
	return a.database.DeleteShareGroup(a.ctx, groupID)
}

func (a *App) GetShareGroupDirs(groupID string) ([]map[string]interface{}, error) {
	dirs, err := a.database.ListShareGroupDirs(a.ctx, groupID)
	if err != nil {
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

func (a *App) StartDownload(targetPubKey, virtualPath, localSavePath string) error {
	if a.netEng == nil {
		return errors.New("network engine not available")
	}
	return a.netEng.StartDownload(targetPubKey, virtualPath, localSavePath)
}

func (a *App) GetRecentHistory(limit, offset int) ([]map[string]interface{}, error) {
	activeTeam := a.crypto.GetActiveTeam()
	var msgs []*db.Message
	var err error
	if activeTeam != "" {
		msgs, err = a.database.GetRecentMessagesByTeam(a.ctx, activeTeam, limit, offset)
	} else {
		msgs, err = a.database.GetRecentMessages(a.ctx, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	var res []map[string]interface{}
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		dec, err := a.crypto.DecryptLocal(m.Content)
		content := "[message corrupted]"
		if err != nil {
			log.Printf("GetRecentHistory: failed to decrypt message %s: %v", m.ID, err)
		} else {
			content = string(dec)
		}
		res = append(res, map[string]interface{}{
			"id": m.ID, "hlc": m.HLC, "sender": m.SenderID, "content": content, "timestamp": m.CreatedAt,
		})
	}
	return res, nil
}

func (a *App) GetRecentHistoryByChannel(channelID string, limit, offset int) ([]map[string]interface{}, error) {
	msgs, err := a.database.GetRecentMessagesByChannel(a.ctx, channelID, limit, offset)
	if err != nil {
		return nil, err
	}
	var res []map[string]interface{}
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		dec, err := a.crypto.DecryptLocal(m.Content)
		content := "[message corrupted]"
		if err != nil {
			log.Printf("GetRecentHistoryByChannel: failed to decrypt message %s: %v", m.ID, err)
		} else {
			content = string(dec)
		}
		res = append(res, map[string]interface{}{
			"id": m.ID, "hlc": m.HLC, "sender": m.SenderID, "channel_id": m.ChannelID, "content": content, "timestamp": m.CreatedAt,
		})
	}
	return res, nil
}

// ConnectToPeer 执行对指定 IP 的三阶段连接测试。
func (a *App) ConnectToPeer(ipAddress string) (map[string]interface{}, error) {
	if a.netEng == nil {
		return nil, errors.New("network engine not available")
	}
	result, err := a.netEng.ConnectToPeer(ipAddress)
	if err != nil {
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
	if a.netEng != nil {
		a.netEng.OnForeground()
	}
}

func mapPhaseResult(r network.PhaseResult) map[string]interface{} {
	return map[string]interface{}{
		"success": r.Success,
		"label":   r.Label,
		"error":   r.Error,
	}
}
