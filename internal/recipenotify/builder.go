// Package recipenotify wires github.com/nikoksr/notify from environment variables for CUE recipe step notifications.
package recipenotify

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/nikoksr/notify"
	notifhttp "github.com/nikoksr/notify/service/http"
	"github.com/nikoksr/notify/service/telegram"
)

const maxSlackTextBytes = 30000

// Env names for optional receivers (secrets belong in env, not in CUE).
const (
	EnvHTTPURL          = "HONEY_NOTIFY_HTTP_URL"
	EnvSlackWebhookURL  = "HONEY_NOTIFY_SLACK_WEBHOOK_URL"
	EnvTelegramBotToken = "HONEY_NOTIFY_TELEGRAM_BOT_TOKEN" // #nosec G101 -- env var key name, not a credential value
	EnvTelegramChatIDs  = "HONEY_NOTIFY_TELEGRAM_CHAT_IDS"
)

// ServiceFilter selects which env-backed backends to register. Nil filter means all configured backends (legacy behavior).
// When Restrict is true, only backends with Allow* true are registered; SlackChannelID is used only when AllowSlack is true.
type ServiceFilter struct {
	Restrict       bool
	AllowHTTP      bool
	AllowSlack     bool
	AllowTelegram  bool
	SlackChannelID string
}

// EnvHasAnyReceiver reports whether any notify-related env var is set (for dry-run hints; do not log values).
func EnvHasAnyReceiver() bool {
	if strings.TrimSpace(os.Getenv(EnvHTTPURL)) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv(EnvSlackWebhookURL)) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv(EnvTelegramBotToken)) != "" && strings.TrimSpace(os.Getenv(EnvTelegramChatIDs)) != "" {
		return true
	}
	return false
}

func envHasHTTPURLs() bool {
	for _, p := range strings.Split(os.Getenv(EnvHTTPURL), ",") {
		if strings.TrimSpace(p) != "" {
			return true
		}
	}
	return false
}

func envHasSlackWebhook() bool {
	return strings.TrimSpace(os.Getenv(EnvSlackWebhookURL)) != ""
}

func envHasTelegram() bool {
	return strings.TrimSpace(os.Getenv(EnvTelegramBotToken)) != "" && strings.TrimSpace(os.Getenv(EnvTelegramChatIDs)) != ""
}

// EnvHasReceiverMatchingFilter reports whether at least one selected backend (per filter) has usable env.
// Nil filter uses EnvHasAnyReceiver().
func EnvHasReceiverMatchingFilter(filter *ServiceFilter) bool {
	if filter == nil || !filter.Restrict {
		return EnvHasAnyReceiver()
	}
	if filter.AllowHTTP && envHasHTTPURLs() {
		return true
	}
	if filter.AllowSlack && envHasSlackWebhook() {
		return true
	}
	if filter.AllowTelegram && envHasTelegram() {
		return true
	}
	return false
}

// BuildFromEnv returns a Notify instance with all configured services, and ok if at least one notifier was added.
func BuildFromEnv() (*notify.Notify, bool) {
	return BuildFromEnvFilter(nil)
}

// BuildFromEnvFilter is like BuildFromEnv but respects filter when non-nil and Restrict is true.
func BuildFromEnvFilter(filter *ServiceFilter) (*notify.Notify, bool) {
	n := notify.New()
	ok := false

	unrestricted := filter == nil || !filter.Restrict
	var allowHTTP, allowSlack, allowTelegram bool
	if unrestricted {
		allowHTTP, allowSlack, allowTelegram = true, true, true
	} else {
		allowHTTP = filter.AllowHTTP
		allowSlack = filter.AllowSlack
		allowTelegram = filter.AllowTelegram
	}
	slackCh := ""
	if filter != nil {
		slackCh = strings.TrimSpace(filter.SlackChannelID)
	}

	if httpSvc := buildHTTPAndSlackServices(allowHTTP, allowSlack, slackCh); httpSvc != nil {
		n.UseServices(httpSvc)
		ok = true
	}

	if allowTelegram {
		if tg := buildTelegram(); tg != nil {
			n.UseServices(tg)
			ok = true
		}
	}

	if !ok {
		return nil, false
	}
	return n, true
}

func buildHTTPAndSlackServices(allowHTTP, allowSlack bool, slackChannelID string) *notifhttp.Service {
	var urls []string
	if allowHTTP {
		for _, p := range strings.Split(os.Getenv(EnvHTTPURL), ",") {
			if u := strings.TrimSpace(p); u != "" {
				urls = append(urls, u)
			}
		}
	}
	slackURL := ""
	if allowSlack {
		slackURL = strings.TrimSpace(os.Getenv(EnvSlackWebhookURL))
	}
	if len(urls) == 0 && slackURL == "" {
		return nil
	}

	svc := notifhttp.New()
	if len(urls) > 0 {
		svc.AddReceiversURLs(urls...)
	}
	if slackURL != "" {
		svc.AddReceivers(slackIncomingWebhook(slackURL, slackChannelID))
	}
	return svc
}

func slackIncomingWebhook(url, channelID string) *notifhttp.Webhook {
	ch := strings.TrimSpace(channelID)
	return &notifhttp.Webhook{
		ContentType:  "application/json; charset=utf-8",
		Header:       http.Header{},
		Method:       http.MethodPost,
		URL:          url,
		BuildPayload: slackWebhookPayloadBuilder(ch),
	}
}

func slackWebhookPayloadBuilder(channelID string) func(subject, message string) any {
	return func(subject, message string) any {
		return slackWebhookPayload(subject, message, channelID)
	}
}

func slackWebhookPayload(subject, message, channelID string) any {
	text := subject + "\n" + message
	if len(text) > maxSlackTextBytes {
		text = text[:maxSlackTextBytes] + "\n...(truncated)"
	}
	if strings.TrimSpace(channelID) == "" {
		return map[string]string{"text": text}
	}
	return map[string]string{
		"text":    text,
		"channel": strings.TrimSpace(channelID),
	}
}

func buildTelegram() *telegram.Telegram {
	token := strings.TrimSpace(os.Getenv(EnvTelegramBotToken))
	raw := strings.TrimSpace(os.Getenv(EnvTelegramChatIDs))
	if token == "" || raw == "" {
		return nil
	}
	var ids []int64
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			zap.L().Debug("recipenotify: skip invalid telegram chat id", zap.String("segment", p), zap.Error(err))
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	tg, err := telegram.New(token)
	if err != nil {
		zap.L().Debug("recipenotify: telegram.New failed, skipping telegram service", zap.Error(err))
		return nil
	}
	tg.AddReceivers(ids...)
	return tg
}
