package app

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"parade/internal/core/crypto"
	"parade/internal/core/db"
	"parade/internal/core/eventbus"
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
	a.registerEventSubscribers()
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

func (a *App) registerEventSubscribers() {
	// 监听节点发现
	a.subscribe(eventbus.TopicPeerJoined, func(c context.Context, ev eventbus.Event) {
		a.ui.Notify("ui_peer_joined", ev.Payload)
	})

	// 监听节点离开
	a.subscribe(eventbus.TopicPeerLeft, func(c context.Context, ev eventbus.Event) {
		a.ui.Notify("ui_peer_left", ev.Payload)
	})

	// 监听收到消息
	a.subscribe(eventbus.TopicMsgReceived, func(c context.Context, ev eventbus.Event) {
		payload := ev.Payload.(eventbus.MsgReceivedPayload)

		// 1. 落盘加密
		encrypted, err := a.crypto.EncryptLocal(payload.Content)
		if err != nil {
			log.Printf("encrypt local failed: %v", err)
			return
		}
		msg := &db.Message{
			ID:        uuid.New().String(),
			HLC:       payload.HLC,
			SenderID:  payload.SenderID,
			Content:   encrypted,
			Type:      payload.Type,
			CreatedAt: ev.Timestamp,
		}
		if err := a.database.InsertMessage(context.Background(), msg); err != nil {
			log.Printf("insert message failed: %v", err)
		}

		// 2. 推送 UI
		a.ui.Notify("ui_new_message", map[string]interface{}{
			"id":        msg.ID,
			"hlc":       msg.HLC,
			"sender":    msg.SenderID,
			"content":   string(payload.Content),
			"timestamp": msg.CreatedAt,
		})
	})

	// 监听文件传输进度
	a.subscribe(eventbus.TopicFileProgress, func(c context.Context, ev eventbus.Event) {
		a.ui.Notify("ui_file_progress", ev.Payload)
	})

	// 监听文件传输完成
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
	a.crypto.SetTeamKey(secret)
	if a.netEng != nil {
		return a.netEng.Start(4327)
	}
	return nil
}

func (a *App) SendTeamChat(text string) error {
	myPub := a.crypto.GetPublicKeyBase64()
	hlc := GenerateHLC(myPub)
	raw := []byte(text)

	// 本地存库
	enc, err := a.crypto.EncryptLocal(raw)
	if err != nil {
		return err
	}
	if err := a.database.InsertMessage(context.Background(), &db.Message{
		ID: uuid.New().String(), HLC: hlc, SenderID: myPub, Content: enc, ReceiverID: db.ReceiverIDGroupChat, CreatedAt: time.Now(),
	}); err != nil {
		log.Printf("insert sent message failed: %v", err)
	}

	// 网络广播
	netMsg := eventbus.MsgReceivedPayload{HLC: hlc, SenderID: myPub, Content: raw}
	jsonBytes, _ := json.Marshal(netMsg)
	teamEnc, err := a.crypto.EncryptTeam(jsonBytes)
	if err != nil {
		return err
	}
	return a.netEng.BroadcastTeam(teamEnc)
}

func (a *App) SendPrivateChat(targetPubKey, text string) error {
	if targetPubKey == "" {
		return errors.New("target public key is required")
	}
	myPub := a.crypto.GetPublicKeyBase64()
	hlc := GenerateHLC(myPub)
	raw := []byte(text)

	// 本地存库
	enc, err := a.crypto.EncryptLocal(raw)
	if err != nil {
		return err
	}
	if err := a.database.InsertMessage(context.Background(), &db.Message{
		ID: uuid.New().String(), HLC: hlc, SenderID: myPub, Content: enc, ReceiverID: targetPubKey, CreatedAt: time.Now(),
	}); err != nil {
		log.Printf("insert private message failed: %v", err)
	}

	// 私聊加密并发送
	netMsg := eventbus.MsgReceivedPayload{HLC: hlc, SenderID: myPub, Content: raw}
	jsonBytes, _ := json.Marshal(netMsg)
	privEnc, err := a.crypto.EncryptPrivate(jsonBytes, targetPubKey)
	if err != nil {
		return err
	}
	return a.netEng.UnicastPrivate(targetPubKey, privEnc)
}

func (a *App) GetPeers() []map[string]string {
	return a.netEng.Peers()
}

func (a *App) ShareDirectory(path string) error {
	if a.fileEng == nil {
		return errors.New("file engine not available")
	}
	return a.fileEng.ShareDirectory(path)
}

func (a *App) UnshareDirectory(path string) error {
	if a.fileEng == nil {
		return errors.New("file engine not available")
	}
	return a.fileEng.UnshareDirectory(path)
}

func (a *App) GetDirectoryChildren(path string) (interface{}, error) {
	if a.fileEng == nil {
		return nil, errors.New("file engine not available")
	}
	return a.fileEng.GetDirectoryChildren(path)
}

func (a *App) GetRecentHistory(limit, offset int) ([]map[string]interface{}, error) {
	msgs, err := a.database.GetRecentMessages(context.Background(), limit, offset)
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
