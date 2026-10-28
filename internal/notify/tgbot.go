package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/system/logger"
)

const (
	tgMuteVerb   = "mute"
	tgUnmuteVerb = "unmute"
	tgMuteIcon   = "🔕"
	tgUnmuteIcon = "🔔"
)

// не const: tgbot_test.go ставит короткие значения, чтобы не ждать по-настоящему
var (
	telegramPollSeconds = 50
	telegramBackoffMin  = 5 * time.Second
	telegramBackoffMax  = 5 * time.Minute
)

var telegramAnswerURL = "https://api.telegram.org/bot%s/answerCallbackQuery"

var telegramEditMarkupURL = "https://api.telegram.org/bot%s/editMessageReplyMarkup"

type tgUpdate struct {
	ID       int64       `json:"update_id"`
	Callback *tgCallback `json:"callback_query"`
}

type tgCallback struct {
	ID      string `json:"id"`
	Data    string `json:"data"`
	Message *struct {
		ID   int64 `json:"message_id"`
		Chat struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"chat"`
		ReplyMarkup *tgKeyboard `json:"reply_markup"`
	} `json:"message"`
}

func (n *Notifier) RunTelegramButtons(ctx context.Context) {
	if !n.cfg.Enabled || n.cfg.TelegramDisabled || !n.telegramReady() || n.mutes == nil {
		return
	}
	hc := &http.Client{
		Timeout:   time.Duration(telegramPollSeconds)*time.Second + sendTimeout,
		Transport: helper.ProxyTransport(n.proxyURL),
	}
	var offset int64
	backoff := telegramBackoffMin
	failing, conflictWarned := false, false
	for ctx.Err() == nil {
		updates, err := n.telegramUpdates(ctx, hc, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var se *statusError
			switch {
			case errors.As(err, &se) && se.status == http.StatusConflict:
				n.tgConflict.Store(true)
				if !conflictWarned {
					logger.Warn("notify: telegram buttons: a webhook is set or another client polls this bot, buttons will not respond and the mute button is hidden: " + err.Error())
					conflictWarned = true
				}
			case !failing:
				logger.Warn("notify: telegram buttons: getUpdates: " + err.Error())
			}
			failing = true
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, telegramBackoffMax)
			continue
		}
		failing, backoff = false, telegramBackoffMin
		n.tgConflict.Store(false)
		for _, raw := range updates {
			var u tgUpdate
			// ошибка типа во вложенном поле не мешает прочитать update_id: смещение двигаем в любом случае
			_ = json.Unmarshal(raw, &u)
			offset = max(offset, u.ID+1)
			if u.Callback != nil {
				n.handleCallback(ctx, u.Callback)
			}
		}
	}
}

func (n *Notifier) telegramUpdates(ctx context.Context, hc *http.Client, offset int64) (_ []json.RawMessage, err error) {
	defer func() { err = redactToken(helper.RedactURLError(err), n.cfg.TelegramToken) }()
	// message нужен намеренно: Telegram помнит последний allowed_updates бота и отбрасывает остальные типы,
	// а ResolveChatID ищет /start именно среди сообщений
	body, err := json.Marshal(map[string]any{
		"offset":          offset,
		"timeout":         telegramPollSeconds,
		"allowed_updates": []string{"message", "callback_query"},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf(telegramGetUpdatesURL, n.cfg.TelegramToken), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= http.StatusBadRequest {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
		return nil, &statusError{status: res.StatusCode, body: string(bytes.TrimSpace(snippet))}
	}
	var parsed struct {
		OK          bool              `json:"ok"`
		Description string            `json:"description"`
		Result      []json.RawMessage `json:"result"`
	}
	if err = json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if !parsed.OK {
		return nil, fmt.Errorf("telegram getUpdates: %s", parsed.Description)
	}
	return parsed.Result, nil
}

func (n *Notifier) chatAllowed(id int64, username string) bool {
	want := strings.TrimSpace(n.cfg.TelegramChatID)
	if name, ok := strings.CutPrefix(want, "@"); ok {
		return username != "" && strings.EqualFold(name, username)
	}
	return strconv.FormatInt(id, 10) == want
}

func (n *Notifier) handleCallback(ctx context.Context, cb *tgCallback) {
	verb, hash, ok := strings.Cut(cb.Data, ":")
	ok = ok && hash != "" && (verb == tgMuteVerb || verb == tgUnmuteVerb)
	if !ok || cb.Message == nil || !n.chatAllowed(cb.Message.Chat.ID, cb.Message.Chat.Username) {
		n.answerCallback(ctx, cb.ID, "")
		return
	}
	muted := verb == tgMuteVerb
	if err := n.mutes.Set(ctx, hash, muted); err != nil {
		logger.Warn("notify: telegram buttons: " + err.Error())
		n.answerCallback(ctx, cb.ID, "")
		return
	}
	m := messages[NormalizeLanguage(n.cfg.Language)]
	toast := m[msgTgUnmuted]
	if muted {
		toast = m[msgTgMuted]
	}
	n.answerCallback(ctx, cb.ID, toast)

	kb := flipMuteButton(cb.Message.ReplyMarkup, cb.Data, hash, muted)
	if kb == nil {
		return
	}
	err := n.post(ctx, fmt.Sprintf(telegramEditMarkupURL, n.cfg.TelegramToken), mustJSON(map[string]any{
		"chat_id":      cb.Message.Chat.ID,
		"message_id":   cb.Message.ID,
		"reply_markup": kb,
	}))
	if err != nil {
		logger.Warn("notify: telegram buttons: editMessageReplyMarkup: " + err.Error())
	}
}

func (n *Notifier) answerCallback(ctx context.Context, id, text string) {
	payload := map[string]any{"callback_query_id": id}
	if text != "" {
		payload["text"] = text
	}
	if err := n.post(ctx, fmt.Sprintf(telegramAnswerURL, n.cfg.TelegramToken), mustJSON(payload)); err != nil {
		logger.Warn("notify: telegram buttons: answerCallbackQuery: " + err.Error())
	}
}

func flipMuteButton(kb *tgKeyboard, data, hash string, muted bool) *tgKeyboard {
	if kb == nil {
		return nil
	}
	flipped := tgButton{Text: tgMuteIcon, CallbackData: tgMuteVerb + ":" + hash}
	if muted {
		flipped = tgButton{Text: tgUnmuteIcon, CallbackData: tgUnmuteVerb + ":" + hash}
	}
	out := &tgKeyboard{InlineKeyboard: make([][]tgButton, len(kb.InlineKeyboard))}
	found := false
	for i, row := range kb.InlineKeyboard {
		out.InlineKeyboard[i] = make([]tgButton, len(row))
		for j, b := range row {
			if b.CallbackData == data {
				b, found = flipped, true
			}
			out.InlineKeyboard[i][j] = b
		}
	}
	if !found {
		return nil
	}
	return out
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
