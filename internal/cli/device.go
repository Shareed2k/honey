package cli

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/mdp/qrterminal/v3"
	"github.com/spf13/cobra"
)

var (
	flagDeviceAdminURL  string
	flagDeviceEnrollURL string
	flagDeviceToken     string
	flagDeviceCN        string
	flagDeviceInsecure  bool
)

var deviceCmd = &cobra.Command{
	Use:   "device",
	Short: "Manage device mTLS enrollment",
}

var deviceEnrollCodeCmd = &cobra.Command{
	Use:   "enroll-code",
	Short: "Mint a one-time device enrollment code and print a QR to scan from the app",
	Long: `Calls the running honey server's admin API to mint a single-use enrollment
code, then prints a QR encoding the bootstrap the mobile app scans: the enroll
URL, the code, and the device CA fingerprint (for pinning).

Examples:
  honey device enroll-code --token "$HONEY_WEB_TOKEN"
  honey device enroll-code --admin-url http://localhost:8765 \
    --enroll-url https://honey.example --cn device:phone-1`,
	RunE: runDeviceEnrollCode,
}

func init() {
	deviceEnrollCodeCmd.Flags().StringVar(&flagDeviceAdminURL, "admin-url", "http://localhost:8765", "Base URL of the running honey server (to mint the code)")
	deviceEnrollCodeCmd.Flags().StringVar(&flagDeviceEnrollURL, "enroll-url", "", "Base URL the app uses to enroll (embedded in the QR); defaults to --admin-url")
	deviceEnrollCodeCmd.Flags().StringVar(&flagDeviceToken, "token", os.Getenv("HONEY_WEB_TOKEN"), "Admin auth token (default $HONEY_WEB_TOKEN)")
	deviceEnrollCodeCmd.Flags().StringVar(&flagDeviceCN, "cn", "", "Device certificate CN (default device:<random>)")
	deviceEnrollCodeCmd.Flags().BoolVar(&flagDeviceInsecure, "insecure", false, "Skip TLS verification when calling the admin URL")

	deviceCmd.AddCommand(deviceEnrollCodeCmd)
	rootCmd.AddCommand(deviceCmd)
}

// mintResponse mirrors handleMintEnrollCode's JSON.
type mintResponse struct {
	Code          string `json:"code"`
	CN            string `json:"cn"`
	EnrollPath    string `json:"enroll_path"`
	CAFingerprint string `json:"ca_fingerprint"`
	CAPem         string `json:"ca_pem"`
	ExpiresIn     int    `json:"expires_in"`
}

// enrollBootstrap is the JSON encoded into the QR the app scans.
type enrollBootstrap struct {
	EnrollURL     string `json:"enroll_url"`
	Code          string `json:"code"`
	CAFingerprint string `json:"ca_fingerprint"`
	CN            string `json:"cn"`
}

func runDeviceEnrollCode(cmd *cobra.Command, _ []string) error {
	adminURL := strings.TrimRight(strings.TrimSpace(flagDeviceAdminURL), "/")
	if adminURL == "" {
		return fmt.Errorf("--admin-url is required")
	}
	enrollBase := strings.TrimRight(strings.TrimSpace(flagDeviceEnrollURL), "/")
	if enrollBase == "" {
		enrollBase = adminURL
	}

	body, _ := json.Marshal(map[string]string{"cn": strings.TrimSpace(flagDeviceCN)})
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost,
		adminURL+"/api/v1/devices/enroll-code", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if t := strings.TrimSpace(flagDeviceToken); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}

	client := &http.Client{}
	if flagDeviceInsecure {
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 -- operator opt-in via --insecure
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("mint enrollment code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mint enrollment code: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var mr mintResponse
	if err := json.Unmarshal(raw, &mr); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	enrollPath := mr.EnrollPath
	if enrollPath == "" {
		enrollPath = "/api/v1/devices/enroll"
	}
	boot := enrollBootstrap{
		EnrollURL:     enrollBase + enrollPath,
		Code:          mr.Code,
		CAFingerprint: mr.CAFingerprint,
		CN:            mr.CN,
	}
	qrJSON, err := json.Marshal(boot)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	qrterminal.GenerateHalfBlock(string(qrJSON), qrterminal.L, out)
	fmt.Fprintf(out, "\nScan the QR from the honey app to enroll this device.\n")
	fmt.Fprintf(out, "  CN:              %s\n", mr.CN)
	fmt.Fprintf(out, "  enroll URL:      %s\n", boot.EnrollURL)
	fmt.Fprintf(out, "  CA fingerprint:  %s\n", mr.CAFingerprint)
	fmt.Fprintf(out, "  expires in:      %ds\n", mr.ExpiresIn)
	return nil
}
