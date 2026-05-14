package ui

import (
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
)

func TestResolveCueNotifyBody(t *testing.T) {
	def := "default"
	if got := resolveCueNotifyBody(nil, def); got != def {
		t.Fatalf("got %q", got)
	}
	n := &cuetry.RecipeNotify{Message: "  x  "}
	if got := resolveCueNotifyBody(n, def); got != "x" {
		t.Fatalf("got %q", got)
	}
}

func TestNotifyServiceFilter_nilServices(t *testing.T) {
	if notifyServiceFilter(&cuetry.RecipeNotify{}) != nil {
		t.Fatal("expected nil filter when services absent")
	}
}

func TestNotifyServiceFilter_allowlist(t *testing.T) {
	slack := &cuetry.RecipeNotifySlack{ChannelID: " C9 "}
	f := notifyServiceFilter(&cuetry.RecipeNotify{
		Services: &cuetry.RecipeNotifyServices{Slack: slack},
	})
	if f == nil || !f.Restrict || !f.AllowSlack || f.AllowHTTP || f.AllowTelegram {
		t.Fatalf("filter: %+v", f)
	}
	if f.SlackChannelID != "C9" {
		t.Fatalf("channel: %q", f.SlackChannelID)
	}
}
