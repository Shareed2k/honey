package apps

import (
	"testing"
)

func TestAppConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		app     AppConfig
		wantErr bool
	}{
		{
			name: "valid http app",
			app: AppConfig{
				Name:      "grafana",
				Type:      AppTypeHTTP,
				Target:    "bastion",
				Upstream:  "grafana.internal:3000",
				LocalPort: 18443,
			},
			wantErr: false,
		},
		{
			name: "valid tcp app",
			app: AppConfig{
				Name:      "postgres",
				Type:      AppTypeTCP,
				Target:    "bastion",
				Upstream:  "postgres.internal:5432",
				LocalPort: 15432,
			},
			wantErr: false,
		},
		{
			name: "missing name",
			app: AppConfig{
				Type:      AppTypeHTTP,
				Target:    "bastion",
				Upstream:  "grafana.internal:3000",
				LocalPort: 18443,
			},
			wantErr: true,
		},
		{
			name: "invalid type",
			app: AppConfig{
				Name:      "grafana",
				Type:      "invalid",
				Target:    "bastion",
				Upstream:  "grafana.internal:3000",
				LocalPort: 18443,
			},
			wantErr: true,
		},
		{
			name: "missing upstream",
			app: AppConfig{
				Name:      "grafana",
				Type:      AppTypeHTTP,
				Target:    "bastion",
				LocalPort: 18443,
			},
			wantErr: true,
		},
		{
			name: "invalid port zero",
			app: AppConfig{
				Name:     "grafana",
				Type:     AppTypeHTTP,
				Target:   "bastion",
				Upstream: "grafana.internal:3000",
			},
			wantErr: true,
		},
		{
			name: "invalid port large",
			app: AppConfig{
				Name:      "grafana",
				Type:      AppTypeHTTP,
				Target:    "bastion",
				Upstream:  "grafana.internal:3000",
				LocalPort: 70000,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.app.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("AppConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
