package engine

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"net"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/nikoksr/notify/service/mail"
	"github.com/shareed2k/honey/internal/cuetry"
)

// RunReporter evaluates mail notification rules at the end of a recipe run
// and sends an HTML summary email if applicable.
type RunReporter struct {
	smtpHost     string
	smtpPort     int
	smtpUsername string
	smtpPassword string
}

// NewRunReporter creates a new RunReporter instance with the provided SMTP configuration.
func NewRunReporter(host string, port int, username, password string) *RunReporter {
	return &RunReporter{
		smtpHost:     host,
		smtpPort:     port,
		smtpUsername: username,
		smtpPassword: password,
	}
}

// Report sends a notification based on the run outcome and recipe configuration.
func (r *RunReporter) Report(ctx context.Context, recipe cuetry.Recipe, results []HostExecResult, runErr error) {
	if r == nil || r.smtpHost == "" {
		return
	}
	zap.L().Info("RunReporter.Report called", zap.Any("notification", recipe.Notification), zap.String("smtpHost", r.smtpHost))

	if recipe.Notification == nil || recipe.Notification.Email == nil || recipe.Notification.Email.SendOn == nil {
		zap.L().Info("RunReporter skipping: notification or send_on is nil")
		return
	}

	success := runErr == nil
	for _, res := range results {
		if !res.Success && !res.Skipped {
			success = false
			zap.L().Info("RunReporter found failure", zap.String("host", res.Name), zap.String("error", res.ErrMsg))
			break
		}
	}

	zap.L().Info("RunReporter outcome evaluated", zap.Bool("success", success), zap.Int("results", len(results)))

	var mailCfg *cuetry.RecipeMailConfig
	if !success && recipe.Notification.Email.SendOn.Failure {
		mailCfg = recipe.Notification.Email.OnFailure
	} else if success && recipe.Notification.Email.SendOn.Success {
		mailCfg = recipe.Notification.Email.OnSuccess
	}

	if mailCfg == nil || len(mailCfg.To) == 0 {
		zap.L().Info("RunReporter skipping: mailCfg is nil or To is empty")
		return
	}

	status := "SUCCEEDED"
	if !success {
		status = "FAILED"
	}

	subject := fmt.Sprintf("%s %s (%s)", mailCfg.Prefix, recipe.Name, status)
	if mailCfg.Prefix == "" {
		subject = fmt.Sprintf("%s (%s)", recipe.Name, status)
	}

	body := r.renderHTML(recipe.Name, status, results, runErr)

	var addr string
	if r.smtpPort > 0 {
		addr = net.JoinHostPort(r.smtpHost, fmt.Sprintf("%d", r.smtpPort))
	} else {
		addr = net.JoinHostPort(r.smtpHost, "587")
	}

	m := mail.New(mailCfg.From, addr)
	if r.smtpUsername != "" || r.smtpPassword != "" {
		m.AuthenticateSMTP("", r.smtpUsername, r.smtpPassword, r.smtpHost)
	}
	m.AddReceivers(mailCfg.To...)
	m.BodyFormat(mail.HTML)

	// Since we are using nikoksr/notify/service/mail, the library does not easily support attachments.
	// For now, if attach_logs is requested, we can append the logs to the HTML body as a <pre> block.
	if mailCfg.AttachLogs {
		body += r.renderLogsHTML(results)
	}

	if err := m.Send(ctx, subject, body); err != nil {
		zap.L().Warn("failed to send recipe completion email", zap.Error(err), zap.String("recipe", recipe.Name))
	} else {
		zap.L().Info("recipe completion email sent", zap.String("recipe", recipe.Name), zap.Strings("to", mailCfg.To))
	}
}

func (r *RunReporter) renderHTML(recipeName, status string, results []HostExecResult, runErr error) string {
	var b bytes.Buffer
	b.WriteString(`<!DOCTYPE html>
<html>
<head>
<style>
body { font-family: -apple-system, sans-serif; padding: 20px; color: #333; }
table { border-collapse: collapse; width: 100%; margin-top: 20px; }
th, td { border: 1px solid #ddd; padding: 12px; text-align: left; }
th { background-color: #f8f9fa; }
.SUCCEEDED { color: green; }
.FAILED { color: red; }
</style>
</head>
<body>`)
	fmt.Fprintf(&b, "<h2>Recipe: %s</h2>", html.EscapeString(recipeName))
	fmt.Fprintf(&b, "<p>Status: <strong class=\"%s\">%s</strong></p>", status, status)
	fmt.Fprintf(&b, "<p>Time: %s</p>", time.Now().Format(time.RFC1123))

	if runErr != nil {
		fmt.Fprintf(&b, "<p class=\"FAILED\">Error: %s</p>", html.EscapeString(runErr.Error()))
	}

	if len(results) > 0 {
		b.WriteString("<table><thead><tr><th>Step</th><th>Host</th><th>Status</th><th>Error</th></tr></thead><tbody>")
		for _, res := range results {
			st := "Success"
			if !res.Success {
				st = "Failed"
			}
			if res.Skipped {
				st = "Skipped"
			}
			fmt.Fprintf(&b, "<tr><td>%d</td><td>%s</td><td class=\"%s\">%s</td><td>%s</td></tr>",
				res.StepIndex, html.EscapeString(res.Name), strings.ToUpper(st), st, html.EscapeString(res.ErrMsg))
		}
		b.WriteString("</tbody></table>")
	}

	b.WriteString("</body></html>")
	return b.String()
}

func (r *RunReporter) renderLogsHTML(results []HostExecResult) string {
	var b bytes.Buffer
	b.WriteString("<hr/><h3>Logs</h3>")
	for _, res := range results {
		if strings.TrimSpace(res.Output) != "" {
			fmt.Fprintf(&b, "<h4>Step %d: %s</h4><pre style=\"background:#f4f4f4;padding:10px;\">%s</pre>",
				res.StepIndex, html.EscapeString(res.Name), html.EscapeString(res.Output))
		}
	}
	return b.String()
}
