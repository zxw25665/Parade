package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"parade/internal/core/crypto"
	"parade/internal/core/db"
	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
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
	logr     logger.Logger

	isLoggedIn bool
	subs       []subscription
}

func NewApp(bus eventbus.EventBus, cry crypto.Engine, d db.Database, net NetworkEngine, file FileEngine, ui Frontend, logr logger.Logger) *App {
	return &App{
		evBus:    bus,
		crypto:   cry,
		database: d,
		netEng:   net,
		fileEng:  file,
		ui:       ui,
		logr:     logr,
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
			a.log(logger.Info, "eventbus", fmt.Sprintf("peer joined: %s", payload.PubKeyBase64))
		}
		a.ui.Notify("ui_peer_joined", ev.Payload)
	})

	a.subscribe(eventbus.TopicPeerLeft, func(c context.Context, ev eventbus.Event) {
		if payload, ok := ev.Payload.(eventbus.PeerEventPayload); ok {
			a.log(logger.Info, "eventbus", fmt.Sprintf("peer left: %s", payload.PubKeyBase64))
		}
		a.ui.Notify("ui_peer_left", ev.Payload)
	})

	a.subscribe(eventbus.TopicMsgReceived, func(c context.Context, ev eventbus.Event) {
		payload, ok := ev.Payload.(eventbus.MsgReceivedPayload)
		if !ok {
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

		encrypted, err := a.crypto.EncryptLocal(payload.Content)
		if err != nil {
			a.log(logger.Warning, "app", fmt.Sprintf("encrypt local failed: %v", err))
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
			a.log(logger.Error, "app", fmt.Sprintf("insert message failed: %v", err))
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

		a.log(logger.Debug, "eventbus", fmt.Sprintf("private msg received from %s type=%d len=%d", payload.SenderID, payload.Type, len(payload.Content)))

		encrypted, err := a.crypto.EncryptLocal(payload.Content)
		if err != nil {
			a.log(logger.Warning, "app", fmt.Sprintf("encrypt local (private) failed: %v", err))
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
			a.log(logger.Error, "app", fmt.Sprintf("insert private message failed: %v", err))
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
	return nil
}

func (a *App) CheckHasIdentity() bool {
	a.log(logger.Debug, "ipc", "CheckHasIdentity called")
	_, err := os.Stat(IdentityFile)
	return !os.IsNotExist(err)
}

func (a *App) JoinTeam(secret string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("JoinTeam called (%d chars)", len(secret)))
	_, err := a.JoinTeamWithName("", secret)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("JoinTeam failed: %v", err))
	}
	return err
}

func (a *App) JoinTeamWithName(name, secret string) (string, error) {
	a.log(logger.Debug, "ipc", fmt.Sprintf("JoinTeamWithName called (name=%s, %d chars)", name, len(secret)))
	teamID := uuid.New().String()
	if name == "" {
		name = "Default Team"
	}

	a.crypto.SetTeamKeyForTeam(teamID, secret)
	if err := a.crypto.SetActiveTeam(teamID); err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("JoinTeamWithName failed: %v", err))
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
		a.log(logger.Warning, "ipc", fmt.Sprintf("JoinTeamWithName failed: %v", err))
		return "", fmt.Errorf("failed to persist team: %w", err)
	}

	if a.netEng != nil {
		if err := a.netEng.Start(4327); err != nil {
			a.log(logger.Warning, "ipc", fmt.Sprintf("JoinTeamWithName failed: %v", err))
			return "", err
		}
	}
	return teamID, nil
}

func (a *App) LeaveTeam(teamID string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("LeaveTeam called (team=%s)", teamID))
	if err := a.database.DeleteTeam(a.ctx, teamID); err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("LeaveTeam failed: %v", err))
		return fmt.Errorf("failed to delete team: %w", err)
	}
	a.crypto.RemoveTeamKey(teamID)
	return nil
}

func (a *App) SwitchTeam(teamID string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("SwitchTeam called (team=%s)", teamID))
	err := a.crypto.SetActiveTeam(teamID)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("SwitchTeam failed: %v", err))
	}
	return err
}

func (a *App) CreateChannel(name string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("CreateChannel called (name=%s)", name))
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
		a.log(logger.Warning, "ipc", fmt.Sprintf("CreateChannel failed: %v", err))
		return fmt.Errorf("failed to create channel: %w", err)
	}
	err := a.database.AddChannelMember(a.ctx, channelID, myPub)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("CreateChannel failed: %v", err))
	}
	return err
}

func (a *App) ListChannels() ([]map[string]interface{}, error) {
	a.log(logger.Debug, "ipc", "ListChannels called")
	activeTeam := a.crypto.GetActiveTeam()
	channels, err := a.database.ListChannelsByTeam(a.ctx, activeTeam)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("ListChannels failed: %v", err))
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
	a.log(logger.Debug, "ipc", fmt.Sprintf("JoinChannel called (channel=%s)", channelID))
	myPub := a.crypto.GetPublicKeyBase64()
	err := a.database.AddChannelMember(a.ctx, channelID, myPub)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("JoinChannel failed: %v", err))
	}
	return err
}

func (a *App) LeaveChannel(channelID string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("LeaveChannel called (channel=%s)", channelID))
	myPub := a.crypto.GetPublicKeyBase64()
	err := a.database.RemoveChannelMember(a.ctx, channelID, myPub)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("LeaveChannel failed: %v", err))
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

func (a *App) GetActiveTeam() string {
	a.log(logger.Debug, "ipc", "GetActiveTeam called")
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
		a.log(logger.Error, "app", fmt.Sprintf("insert message failed: %v", err))
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
	a.log(logger.Debug, "ipc", fmt.Sprintf("SendTeamChat called (%d chars)", len(text)))
	err := a.sendMessageWith(text, db.ReceiverIDGroupChat, "", a.crypto.EncryptTeam, a.netEng.BroadcastTeam)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("SendTeamChat failed: %v", err))
	}
	return err
}

func (a *App) SendPrivateChat(targetPubKey, text string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("SendPrivateChat called (to=%s, %d chars)", targetPubKey, len(text)))
	if targetPubKey == "" {
		return errors.New("target public key is required")
	}
	err := a.sendMessageWith(text, targetPubKey, "",
		func(payload []byte) ([]byte, error) { return a.crypto.EncryptPrivate(payload, targetPubKey) },
		func(payload []byte) error { return a.netEng.UnicastPrivate(targetPubKey, payload) },
	)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("SendPrivateChat failed: %v", err))
	}
	return err
}

func (a *App) SendChannelChat(channelID, text string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("SendChannelChat called (channel=%s, %d chars)", channelID, len(text)))
	err := a.sendMessageWith(text, db.ReceiverIDGroupChat, channelID, a.crypto.EncryptTeam, func(payload []byte) error {
		return a.netEng.BroadcastChannel(channelID, payload)
	})
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("SendChannelChat failed: %v", err))
	}
	return err
}

func (a *App) GetPeers() []map[string]string {
	a.log(logger.Debug, "ipc", "GetPeers called")
	return a.netEng.Peers()
}

func (a *App) ShareDirectory(path string) error {
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
func (a *App) GetRemoteDirectoryChildren(targetPubKey, path string) ([]map[string]interface{}, error) {
	a.log(logger.Debug, "ipc", fmt.Sprintf("GetRemoteDirectoryChildren called (to=%s, path=%s)", targetPubKey, path))
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

func (a *App) StartDownload(targetPubKey, virtualPath, localSavePath string) error {
	a.log(logger.Debug, "ipc", fmt.Sprintf("StartDownload called (to=%s, file=%s)", targetPubKey, virtualPath))
	if a.netEng == nil {
		return errors.New("network engine not available")
	}
	err := a.netEng.StartDownload(targetPubKey, virtualPath, localSavePath)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("StartDownload failed: %v", err))
	}
	return err
}

func (a *App) GetRecentHistory(limit, offset int) ([]map[string]interface{}, error) {
	a.log(logger.Debug, "ipc", fmt.Sprintf("GetRecentHistory called (limit=%d, offset=%d)", limit, offset))
	activeTeam := a.crypto.GetActiveTeam()
	var msgs []*db.Message
	var err error
	if activeTeam != "" {
		msgs, err = a.database.GetRecentMessagesByTeam(a.ctx, activeTeam, limit, offset)
	} else {
		msgs, err = a.database.GetRecentMessages(a.ctx, limit, offset)
	}
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("GetRecentHistory failed: %v", err))
		return nil, err
	}
	var res []map[string]interface{}
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		dec, err := a.crypto.DecryptLocal(m.Content)
		content := "[message corrupted]"
		if err != nil {
			a.log(logger.Warning, "app", fmt.Sprintf("GetRecentHistory: failed to decrypt message %s: %v", m.ID, err))
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
	a.log(logger.Debug, "ipc", fmt.Sprintf("GetRecentHistoryByChannel called (channel=%s, limit=%d, offset=%d)", channelID, limit, offset))
	msgs, err := a.database.GetRecentMessagesByChannel(a.ctx, channelID, limit, offset)
	if err != nil {
		a.log(logger.Warning, "ipc", fmt.Sprintf("GetRecentHistoryByChannel failed: %v", err))
		return nil, err
	}
	var res []map[string]interface{}
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		dec, err := a.crypto.DecryptLocal(m.Content)
		content := "[message corrupted]"
		if err != nil {
			a.log(logger.Warning, "app", fmt.Sprintf("GetRecentHistoryByChannel: failed to decrypt message %s: %v", m.ID, err))
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
