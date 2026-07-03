package sync

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"parade/internal/core/db"
)

// DatasetConfig controls how test datasets are generated.
type DatasetConfig struct {
	TotalMessages int      // total messages in the dataset
	Conversations []string // conversation IDs to distribute messages across
	StartTime     time.Time // earliest message time
	HoursSpan     int      // spread messages across this many hours
	NodeIDHint    string   // node ID suffix for HLC generation
}

// DefaultDatasetA: 500 messages, 1 conversation, 72-hour span.
var DefaultDatasetA = DatasetConfig{
	TotalMessages: 500,
	Conversations: []string{"conv-a"},
	StartTime:     time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
	HoursSpan:     72,
	NodeIDHint:    "node_a",
}

// DefaultDatasetB: 500 messages, 2 conversations, 48-hour span.
var DefaultDatasetB = DatasetConfig{
	TotalMessages: 500,
	Conversations: []string{"conv-b1", "conv-b2"},
	StartTime:     time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	HoursSpan:     48,
	NodeIDHint:    "node_b",
}

// GenerateMessages creates a deterministic message set from a config.
// Messages are spread evenly across conversations and time.
func GenerateMessages(cfg DatasetConfig) []*db.Message {
	rng := rand.New(rand.NewSource(int64(cfg.TotalMessages) + cfg.StartTime.Unix()))
	msgs := make([]*db.Message, cfg.TotalMessages)
	convCount := len(cfg.Conversations)

	for i := 0; i < cfg.TotalMessages; i++ {
		convIdx := i % convCount
		hourOffset := (i * cfg.HoursSpan) / cfg.TotalMessages
		minute := rng.Intn(60)
		second := rng.Intn(60)
		ms := rng.Intn(1000)
		counter := rng.Intn(10000)

		t := cfg.StartTime.Add(time.Duration(hourOffset) * time.Hour).
			Add(time.Duration(minute) * time.Minute).
			Add(time.Duration(second) * time.Second).
			Add(time.Duration(ms) * time.Millisecond)

		hlc := fmt.Sprintf("%s_%04d_%s",
			t.Format("2006-01-02T15:04:05.000Z"),
			counter,
			cfg.NodeIDHint)

		content := fmt.Sprintf("msg_%d_from_%s_conv_%s_data_%x",
			i, cfg.NodeIDHint, cfg.Conversations[convIdx], rng.Uint64())

		msgs[i] = &db.Message{
			ID:             uuid.New().String(),
			HLC:            hlc,
			SenderID:       cfg.NodeIDHint,
			ReceiverID:     "",
			TeamID:         "test-team",
			ConversationID: cfg.Conversations[convIdx],
			Content:        []byte(content),
			Type:           0,
			CreatedAt:      t,
		}
	}
	return msgs
}

// CloneMessages deep-copies a message slice (for giving each node its own copy).
func CloneMessages(src []*db.Message) []*db.Message {
	dst := make([]*db.Message, len(src))
	for i, m := range src {
		c := *m
		c.Content = append([]byte(nil), m.Content...)
		dst[i] = &c
	}
	return dst
}

// SubsetMessages returns a random subset of messages (for simulating partial sync).
func SubsetMessages(src []*db.Message, fraction float64, seed int64) []*db.Message {
	rng := rand.New(rand.NewSource(seed))
	n := int(float64(len(src)) * fraction)
	if n < 1 {
		n = 1
	}
	if n > len(src) {
		n = len(src)
	}
	perm := rng.Perm(len(src))[:n]
	result := make([]*db.Message, n)
	for i, idx := range perm {
		c := *src[idx]
		c.Content = append([]byte(nil), src[idx].Content...)
		result[i] = &c
	}
	return result
}

// SortMessagesByHLC sorts messages by HLC ascending.
func SortMessagesByHLC(msgs []*db.Message) {
	for i := 0; i < len(msgs); i++ {
		for j := i + 1; j < len(msgs); j++ {
			if msgs[i].HLC > msgs[j].HLC {
				msgs[i], msgs[j] = msgs[j], msgs[i]
			}
		}
	}
}
