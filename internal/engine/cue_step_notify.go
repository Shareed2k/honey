package engine

import (
	"context"
	"fmt"
	"io"
	"strings"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/recipenotify"
)

// maxCueNotifyMessageRunes caps notify body size before Send (Slack path truncates again in recipenotify).
const maxCueNotifyMessageRunes = 120_000

func truncateCueNotifyBody(s string) string {
	runes := []rune(s)
	if len(runes) <= maxCueNotifyMessageRunes {
		return s
	}
	const banner = "\n…(message truncated for notify)…\n"
	bannerR := []rune(banner)
	keep := maxCueNotifyMessageRunes - len(bannerR)
	if keep < 100 {
		return string(runes[:maxCueNotifyMessageRunes])
	}
	half := keep / 2
	return string(runes[:half]) + banner + string(runes[len(runes)-(keep-half):])
}

func resolveCueNotifyBody(notify *cuetry.RecipeNotify, defaultBody string) string {
	if notify == nil {
		return defaultBody
	}
	if m := strings.TrimSpace(notify.Message); m != "" {
		return m
	}
	return defaultBody
}

func resolveCueNotifySubject(recipe cuetry.Recipe, stepNo int, kind string, notify *cuetry.RecipeNotify) string {
	if notify == nil {
		return ""
	}
	if s := strings.TrimSpace(notify.NotifySubject); s != "" {
		return s
	}
	name := strings.TrimSpace(recipe.Name)
	if kind == cuetry.KindAI {
		return fmt.Sprintf("honey: %s AI summary", name)
	}
	return fmt.Sprintf("honey: %s step %d (%s)", name, stepNo, kind)
}

// notifyServiceFilter maps recipe notify.services to recipenotify.ServiceFilter. Nil notify or nil Services means no restriction (all env backends).
func notifyServiceFilter(notify *cuetry.RecipeNotify) *recipenotify.ServiceFilter {
	if notify == nil || notify.Services == nil {
		return nil
	}
	s := notify.Services
	ch := ""
	if s.Slack != nil {
		ch = strings.TrimSpace(s.Slack.ChannelID)
	}
	return &recipenotify.ServiceFilter{
		Restrict:       true,
		AllowHTTP:      s.HTTP != nil,
		AllowSlack:     s.Slack != nil,
		AllowTelegram:  s.Telegram != nil,
		SlackChannelID: ch,
	}
}

func cueNotifyNoReceiversHint(filter *recipenotify.ServiceFilter) string {
	if filter != nil && filter.Restrict {
		return "\n\n(notify: no service in notify.services has matching env configuration — set HONEY_NOTIFY_* for the selected services; see honey cue-exec docs)"
	}
	return "\n\n(notify: enabled but no receivers configured — set " + recipenotify.EnvHTTPURL + ", " + recipenotify.EnvSlackWebhookURL + ", and/or " + recipenotify.EnvTelegramBotToken + " + " + recipenotify.EnvTelegramChatIDs + ")"
}

// CueStepNotifyAppendSuffix sends notify after a successful AI step; returns text to append on missing receivers or send errors.
// CueStepNotifyAppendSuffix ...
func CueStepNotifyAppendSuffix(ctx context.Context, recipe cuetry.Recipe, stepNo int, kind string, notify *cuetry.RecipeNotify, body string) string {
	if notify == nil {
		return ""
	}
	subject := resolveCueNotifySubject(recipe, stepNo, kind, notify)
	body = truncateCueNotifyBody(resolveCueNotifyBody(notify, body))
	filter := notifyServiceFilter(notify)
	if !recipenotify.EnvHasReceiverMatchingFilter(filter) {
		return cueNotifyNoReceiversHint(filter)
	}
	n, ok := recipenotify.BuildFromEnvFilter(filter)
	if !ok {
		return cueNotifyNoReceiversHint(filter)
	}
	if err := n.Send(ctx, subject, body); err != nil {
		return fmt.Sprintf("\n\n(notify failed: %v)", err)
	}
	return ""
}

// CueStepNotifyRemote sends notify after a non-AI step; failures are logged only (no change to streamed host rows).
// CueStepNotifyRemote ...
func CueStepNotifyRemote(ctx context.Context, recipe cuetry.Recipe, stepNo int, kind string, notify *cuetry.RecipeNotify, body string) {
	if notify == nil {
		return
	}
	subject := resolveCueNotifySubject(recipe, stepNo, kind, notify)
	body = truncateCueNotifyBody(resolveCueNotifyBody(notify, body))
	filter := notifyServiceFilter(notify)
	if !recipenotify.EnvHasReceiverMatchingFilter(filter) {
		zap.L().Warn("cue recipe notify: no matching receivers for configuration",
			zap.Int("step", stepNo),
			zap.String("kind", kind),
			zap.Bool("services_restrict", filter != nil && filter.Restrict))
		return
	}
	n, ok := recipenotify.BuildFromEnvFilter(filter)
	if !ok {
		zap.L().Warn("cue recipe notify: no notifier built",
			zap.Int("step", stepNo),
			zap.String("kind", kind))
		return
	}
	if err := n.Send(ctx, subject, body); err != nil {
		zap.L().Warn("cue recipe notify: send failed",
			zap.Int("step", stepNo),
			zap.String("kind", kind),
			zap.Error(err))
	}
}

// CueHookNotifyRemote sends notify after a per-host hook; failures are logged only.
// CueHookNotifyRemote ...
func CueHookNotifyRemote(ctx context.Context, recipe cuetry.Recipe, stepNo int, kind string, phase, hostName string, notify *cuetry.RecipeNotify, body string) {
	if notify == nil {
		return
	}
	subject := strings.TrimSpace(notify.NotifySubject)
	if subject == "" {
		name := strings.TrimSpace(recipe.Name)
		subject = fmt.Sprintf("honey: hook %s step %d (%s) %s host %q", name, stepNo, kind, phase, hostName)
	}
	body = truncateCueNotifyBody(resolveCueNotifyBody(notify, body))
	filter := notifyServiceFilter(notify)
	if !recipenotify.EnvHasReceiverMatchingFilter(filter) {
		zap.L().Warn("cue recipe hook notify: no matching receivers for configuration",
			zap.Int("step", stepNo),
			zap.String("phase", phase),
			zap.String("host", hostName),
			zap.Bool("services_restrict", filter != nil && filter.Restrict))
		return
	}
	n, ok := recipenotify.BuildFromEnvFilter(filter)
	if !ok {
		zap.L().Warn("cue recipe hook notify: no notifier built",
			zap.Int("step", stepNo),
			zap.String("phase", phase),
			zap.String("host", hostName))
		return
	}
	if err := n.Send(ctx, subject, body); err != nil {
		zap.L().Warn("cue recipe hook notify: send failed",
			zap.Int("step", stepNo),
			zap.String("phase", phase),
			zap.String("host", hostName),
			zap.Error(err))
	}
}

// WriteCueStepNotifyDryLine prints one plan line when notify is enabled (boolean only; no secrets).
// WriteCueStepNotifyDryLine ...
func WriteCueStepNotifyDryLine(out io.Writer, step cuetry.Step) {
	if !step.Base().NotifyEnabled() {
		return
	}
	filter := notifyServiceFilter(step.Base().Notify)
	hasRecv := recipenotify.EnvHasReceiverMatchingFilter(filter)
	_, _ = fmt.Fprintf(out, "  notify: enabled — receivers configured (env)=%v (HTTP/Slack/Telegram — see docs; values not shown)\n", hasRecv)
}
