package app

import (
	"context"
	"encoding/json"
//	"fmt"
	"log"
	"os"
//	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"parade/internal/core/crypto"
	"parade/internal/core/db"
	"parade/internal/core/eventbus"
)

const IdentityFile = "./.parade_identity"

type App struct {
	ctx      context.Context
	evBus    eventbus.EventBus
	crypto   crypto.Engine
	database db.Database
	netEng   NetworkEngine
	fileEng  FileEngine
	ui       Frontend // 抽象的前端接口

	isLoggedIn bool
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

func (a *App) registerEventSubscribers() {
	// 监听节点发现
	a.evBus.Subscribe(eventbus.TopicPeerJoined, func(c context.Context, ev eventbus.Event) {
		a.ui.Notify("ui_peer_joined", ev.Payload)
	})

	// 监听收到消息
	a.evBus.Subscribe(eventbus.TopicMsgReceived, func(c context.Context, ev eventbus.Event) {
		payload := ev.Payload.(eventbus.MsgReceivedPayload)

		// 1. 落盘加密
		encrypted, _ := a.crypto.EncryptLocal(payload.Content)
		msg := &db.Message{
			ID:        uuid.New().String(),
			HLC:       payload.HLC,
			SenderID:  payload.SenderID,
			Content:   encrypted,
			Type:      payload.Type,
			CreatedAt: ev.Timestamp,
		}
		_ = a.database.InsertMessage(context.Background(), msg)

		// 2. 推送 UI
		a.ui.Notify("ui_new_message", map[string]interface{}{
			"id":        msg.ID,
			"hlc":       msg.HLC,
			"sender":    msg.SenderID,
			"content":   string(payload.Content),
			"timestamp": msg.CreatedAt,
		})
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
	enc, _ := a.crypto.EncryptLocal(raw)
	_ = a.database.InsertMessage(context.Background(), &db.Message{
		ID: uuid.New().String(), HLC: hlc, SenderID: myPub, Content: enc, CreatedAt: time.Now(),
	})

	// 网络广播
	netMsg := eventbus.MsgReceivedPayload{HLC: hlc, SenderID: myPub, Content: raw}
	jsonBytes, _ := json.Marshal(netMsg)
	teamEnc, _ := a.crypto.EncryptTeam(jsonBytes)
	
	return a.netEng.BroadcastTeam(teamEnc)
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
