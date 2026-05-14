package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	jose "github.com/go-jose/go-jose/v4"
)

const (
	sessionMaxLineBytes      = 32 << 20
	sessionProtocolVersionV1 = 1
)

type sessionKeyReady struct {
	Type      string `json:"type"`
	Protocol  int    `json:"protocol"`
	KID       string `json:"kid"`
	PublicJWK string `json:"public_jwk"`
}

type sessionWireResult struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type sessionHostMsg struct {
	Op          string `json:"op"`
	CredsJWE    string `json:"creds_jwe,omitempty"`
	ProbeAccess string `json:"probe_access,omitempty"`
	Path        string `json:"path,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Bucket      string `json:"bucket,omitempty"`
	Object      string `json:"object,omitempty"`
	Region      string `json:"region,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
}

func writeJSONLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err = w.Write([]byte{'\n'})
	return err
}

func readLineLimited(r *bufio.Reader, maxBytes int) ([]byte, error) {
	part, err := r.ReadBytes('\n')
	if len(part) > maxBytes {
		return nil, fmt.Errorf("line exceeds maximum size %d", maxBytes)
	}
	if err != nil {
		if err == io.EOF && len(part) > 0 {
			return part, nil
		}
		return nil, err
	}
	return part, nil
}

func transferArgsFromSessionMsg(m sessionHostMsg) transferArgs {
	return transferArgs{
		Path:        strings.TrimSpace(m.Path),
		Provider:    strings.TrimSpace(m.Provider),
		Bucket:      strings.TrimSpace(m.Bucket),
		Object:      strings.TrimSpace(m.Object),
		Region:      strings.TrimSpace(m.Region),
		Endpoint:    strings.TrimSpace(m.Endpoint),
		ProbeAccess: strings.TrimSpace(m.ProbeAccess),
	}
}

func sessionTryBootstrap(priv *ecdsa.PrivateKey, msg sessionHostMsg, claims **credentialClaims) error {
	if *claims != nil {
		return writeJSONLine(os.Stdout, sessionWireResult{OK: false, Error: "already bootstrapped"})
	}
	c, derr := decryptClaimsWithKey(priv, msg.CredsJWE)
	if derr != nil {
		return writeJSONLine(os.Stdout, sessionWireResult{OK: false, Error: derr.Error()})
	}
	*claims = c
	return writeJSONLine(os.Stdout, sessionWireResult{OK: true, Detail: "bootstrapped"})
}

func sessionTryClose(claims **credentialClaims) (stop bool, err error) {
	if *claims == nil {
		return false, writeJSONLine(os.Stdout, sessionWireResult{OK: false, Error: "not bootstrapped"})
	}
	if err := writeJSONLine(os.Stdout, sessionWireResult{OK: true, Detail: "bye"}); err != nil {
		return false, err
	}
	return true, nil
}

func sessionRunCloudTransfer(ctx context.Context, op string, msg sessionHostMsg, claims *credentialClaims) error {
	if claims == nil {
		return writeJSONLine(os.Stdout, sessionWireResult{OK: false, Error: "not bootstrapped"})
	}
	a := transferArgsFromSessionMsg(msg)
	cli, cerr := newCloudClient(ctx, a, claims)
	if cerr != nil {
		return writeJSONLine(os.Stdout, sessionWireResult{OK: false, Error: cerr.Error()})
	}
	var runErr error
	switch op {
	case "probe":
		access := strings.ToLower(strings.TrimSpace(a.ProbeAccess))
		if access == "" {
			access = "read"
		}
		switch access {
		case "write":
			runErr = cli.ProbeWrite(ctx, a)
		case "read":
			runErr = cli.ProbeRead(ctx, a)
		default:
			runErr = fmt.Errorf("invalid probe_access %q", a.ProbeAccess)
		}
	case "upload":
		runErr = cli.Upload(ctx, a)
	case "download":
		runErr = cli.Download(ctx, a)
	case "cleanup":
		runErr = cli.Cleanup(ctx, a)
	default:
		runErr = fmt.Errorf("unsupported cloud op %q", op)
	}
	if runErr != nil {
		return writeJSONLine(os.Stdout, sessionWireResult{OK: false, Error: runErr.Error()})
	}
	return writeJSONLine(os.Stdout, sessionWireResult{OK: true, Detail: op + " ok"})
}

func runSession(ctx context.Context) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	kidBytes := make([]byte, 12)
	if _, err := rand.Read(kidBytes); err != nil {
		return err
	}
	kid := hex.EncodeToString(kidBytes)
	jwk := jose.JSONWebKey{
		Key:       &priv.PublicKey,
		KeyID:     kid,
		Use:       "enc",
		Algorithm: string(jose.ECDH_ES),
	}
	pubRaw, err := json.Marshal(jwk)
	if err != nil {
		return err
	}
	keyOut := sessionKeyReady{
		Type:      "key_ready",
		Protocol:  sessionProtocolVersionV1,
		KID:       kid,
		PublicJWK: string(pubRaw),
	}
	if err := writeJSONLine(os.Stdout, keyOut); err != nil {
		return err
	}
	br := bufio.NewReaderSize(os.Stdin, 256*1024)
	var claims *credentialClaims

	for {
		rawLine, err := readLineLimited(br, sessionMaxLineBytes)
		if err == io.EOF {
			if claims == nil {
				return fmt.Errorf("eof before bootstrap")
			}
			return fmt.Errorf("eof before close")
		}
		if err != nil {
			return err
		}
		var msg sessionHostMsg
		if err := json.Unmarshal(rawLine, &msg); err != nil {
			if werr := writeJSONLine(os.Stdout, sessionWireResult{OK: false, Error: "invalid json: " + err.Error()}); werr != nil {
				return werr
			}
			continue
		}
		op := strings.ToLower(strings.TrimSpace(msg.Op))
		switch op {
		case "bootstrap":
			if err := sessionTryBootstrap(priv, msg, &claims); err != nil {
				return err
			}
		case "close":
			stop, cerr := sessionTryClose(&claims)
			if cerr != nil {
				return cerr
			}
			if stop {
				return nil
			}
		case "probe", "upload", "download", "cleanup":
			if err := sessionRunCloudTransfer(ctx, op, msg, claims); err != nil {
				return err
			}
		default:
			if werr := writeJSONLine(os.Stdout, sessionWireResult{OK: false, Error: fmt.Sprintf("unknown op %q", msg.Op)}); werr != nil {
				return werr
			}
		}
	}
}
