package proxmoxprovider

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"

	"github.com/Telmate/proxmox-api-go/proxmox"
)

func dialTelmate(ctx context.Context, b ProxmoxBackendRuntime) (*proxmox.Client, error) {
	if strings.TrimSpace(b.URL) == "" {
		return nil, fmt.Errorf("proxmox: empty API URL")
	}
	tlsCfg := &tls.Config{InsecureSkipVerify: b.Insecure} // #nosec G402 -- explicit user config
	c, err := proxmox.NewClient(b.URL, nil, "", tlsCfg, "", 300, false)
	if err != nil {
		return nil, err
	}
	switch {
	case strings.TrimSpace(b.TokenID) != "":
		var tokenID proxmox.ApiTokenID
		if err := tokenID.Parse(b.TokenID); err != nil {
			return nil, fmt.Errorf("proxmox token id: %w", err)
		}
		c.SetAPIToken(tokenID, proxmox.ApiTokenSecret(b.TokenSec))
	case strings.TrimSpace(b.User) != "":
		if err := c.Login(ctx, b.User, b.Password, ""); err != nil {
			return nil, fmt.Errorf("proxmox login: %w", err)
		}
	default:
		return nil, fmt.Errorf("proxmox: need token or user/password")
	}
	return c, nil
}
