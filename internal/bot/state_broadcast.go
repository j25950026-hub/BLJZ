package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

const (
	defaultStateBroadcastChannelPrefix = "channel:group:"
	stateWebSocketWriteTimeout         = 5 * time.Second
	stateWebSocketPingInterval         = 25 * time.Second
	stateWebSocketSubscriberBuffer     = 16
)

type LedgerReversalStateEvent struct {
	Event          string `json:"event"`
	ChatID         int64  `json:"chat_id"`
	MessageID      int    `json:"message_id"`
	OperatorUserID int64  `json:"operator_user_id"`
	Command        string `json:"command"`
	OriginalID     int64  `json:"original_id"`
	ReversalID     int64  `json:"reversal_id"`
	OccurredAt     string `json:"occurred_at"`
}

type StateBroadcaster interface {
	BroadcastReversal(ctx context.Context, event LedgerReversalStateEvent) error
}

type noopStateBroadcaster struct{}

func (noopStateBroadcaster) BroadcastReversal(context.Context, LedgerReversalStateEvent) error {
	return nil
}

type fanoutStateBroadcaster struct {
	targets []StateBroadcaster
}

func NewFanoutStateBroadcaster(targets ...StateBroadcaster) StateBroadcaster {
	filtered := make([]StateBroadcaster, 0, len(targets))
	for _, target := range targets {
		if target == nil {
			continue
		}
		filtered = append(filtered, target)
	}
	switch len(filtered) {
	case 0:
		return noopStateBroadcaster{}
	case 1:
		return filtered[0]
	default:
		return &fanoutStateBroadcaster{targets: filtered}
	}
}

func (f *fanoutStateBroadcaster) BroadcastReversal(ctx context.Context, event LedgerReversalStateEvent) error {
	var errs []error
	for _, target := range f.targets {
		if err := target.BroadcastReversal(ctx, event); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

type wsStateSubscriber struct {
	events chan LedgerReversalStateEvent
}

type WebSocketStateHub struct {
	mu               sync.RWMutex
	subscribers      map[int64]map[*wsStateSubscriber]struct{}
	subscriberBuffer int
	upgrader         websocket.Upgrader
}

func NewWebSocketStateHub() *WebSocketStateHub {
	return &WebSocketStateHub{
		subscribers:      map[int64]map[*wsStateSubscriber]struct{}{},
		subscriberBuffer: stateWebSocketSubscriberBuffer,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(*http.Request) bool {
				return true
			},
		},
	}
}

func (h *WebSocketStateHub) Subscribe(chatID int64) (<-chan LedgerReversalStateEvent, func()) {
	sub := &wsStateSubscriber{events: make(chan LedgerReversalStateEvent, h.subscriberBuffer)}
	h.mu.Lock()
	if h.subscribers[chatID] == nil {
		h.subscribers[chatID] = map[*wsStateSubscriber]struct{}{}
	}
	h.subscribers[chatID][sub] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			group := h.subscribers[chatID]
			if group == nil {
				return
			}
			delete(group, sub)
			if len(group) == 0 {
				delete(h.subscribers, chatID)
			}
		})
	}
	return sub.events, unsubscribe
}

func (h *WebSocketStateHub) BroadcastReversal(ctx context.Context, event LedgerReversalStateEvent) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(event.OccurredAt) == "" {
		event.OccurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	for _, sub := range h.snapshotSubscribers(event.ChatID) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sub.events <- event:
		default:
		}
	}
	return nil
}

func (h *WebSocketStateHub) snapshotSubscribers(chatID int64) []*wsStateSubscriber {
	h.mu.RLock()
	defer h.mu.RUnlock()
	list := make([]*wsStateSubscriber, 0, len(h.subscribers[chatID])+len(h.subscribers[0]))
	for sub := range h.subscribers[chatID] {
		list = append(list, sub)
	}
	if chatID != 0 {
		for sub := range h.subscribers[0] {
			list = append(list, sub)
		}
	}
	return list
}

func (h *WebSocketStateHub) ServeWS(w http.ResponseWriter, r *http.Request, chatID int64) error {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	events, unsubscribe := h.Subscribe(chatID)
	defer unsubscribe()

	readErrCh := make(chan error, 1)
	go func() {
		for {
			if _, _, readErr := conn.ReadMessage(); readErr != nil {
				readErrCh <- readErr
				return
			}
		}
	}()

	pingTicker := time.NewTicker(stateWebSocketPingInterval)
	defer pingTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return nil
		case readErr := <-readErrCh:
			return readErr
		case event := <-events:
			if err := conn.SetWriteDeadline(time.Now().Add(stateWebSocketWriteTimeout)); err != nil {
				return err
			}
			if err := conn.WriteJSON(event); err != nil {
				return err
			}
		case <-pingTicker.C:
			if err := conn.SetWriteDeadline(time.Now().Add(stateWebSocketWriteTimeout)); err != nil {
				return err
			}
			if err := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(stateWebSocketWriteTimeout)); err != nil {
				return err
			}
		}
	}
}

type RedisStateBroadcasterConfig struct {
	Addr           string
	Password       string
	DB             int
	ChannelPrefix  string
	PublishTimeout time.Duration
}

type RedisStateBroadcaster struct {
	client         *redis.Client
	channelPrefix  string
	publishTimeout time.Duration
}

func NewRedisStateBroadcaster(cfg RedisStateBroadcasterConfig) (*RedisStateBroadcaster, error) {
	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		return nil, fmt.Errorf("redis 地址不能为空")
	}
	channelPrefix := strings.TrimSpace(cfg.ChannelPrefix)
	if channelPrefix == "" {
		channelPrefix = defaultStateBroadcastChannelPrefix
	}
	publishTimeout := cfg.PublishTimeout
	if publishTimeout <= 0 {
		publishTimeout = 2 * time.Second
	}
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: strings.TrimSpace(cfg.Password),
		DB:       cfg.DB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &RedisStateBroadcaster{
		client:         client,
		channelPrefix:  channelPrefix,
		publishTimeout: publishTimeout,
	}, nil
}

func (r *RedisStateBroadcaster) Channel(chatID int64) string {
	return r.channelPrefix + strconv.FormatInt(chatID, 10)
}

func (r *RedisStateBroadcaster) BroadcastReversal(ctx context.Context, event LedgerReversalStateEvent) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(event.OccurredAt) == "" {
		event.OccurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	publishCtx, cancel := context.WithTimeout(ctx, r.publishTimeout)
	defer cancel()
	return r.client.Publish(publishCtx, r.Channel(event.ChatID), payload).Err()
}

func (r *RedisStateBroadcaster) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}
