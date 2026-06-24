package secrets

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/hashicorp/vault/api"
	"github.com/zalando/go-keyring"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// ExtraBackend allows registering custom secret resolvers (like plugins).
// It returns (value, handled, error).
type ExtraBackend func(ctx context.Context, ref string) (string, bool, error)

// Resolver resolves a full recipe secrets ref string to plaintext.
type Resolver struct {
	opts Options
}

// NewResolver builds the default Resolver from Options.
func NewResolver(opts Options) (*Resolver, error) {
	return &Resolver{opts: opts}, nil
}

// Handles reports whether this resolver can handle the ref.
func (r *Resolver) Handles(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	for _, b := range r.opts.ExtraBackends {
		if _, ok, _ := b(context.Background(), ref); ok {
			return true
		}
	}
	prefixes := []string{"env:", "k8s:", "aws-kms:", "aws-sm:", "vault:", "keyring://", "age:", "age-b64:", "age-file:"}
	for _, p := range prefixes {
		if strings.HasPrefix(ref, p) {
			return true
		}
	}
	// secure: is handled by the stack resolver which we'll integrate or handle differently?
	// secure: is symmetric decrypted by the stack package. Let's include it.
	if strings.HasPrefix(ref, "secure:") {
		return true
	}
	return false
}

// Resolve implements the core secret resolution loop.
func (r *Resolver) Resolve(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("empty secret ref")
	}

	for _, b := range r.opts.ExtraBackends {
		if val, ok, err := b(ctx, ref); ok {
			return val, err
		}
	}

	switch {
	case strings.HasPrefix(ref, "env:"):
		name := strings.TrimSpace(ref[len("env:"):])
		if name == "" {
			return "", fmt.Errorf("env: ref missing variable name")
		}
		v := os.Getenv(name)
		if v == "" {
			return "", fmt.Errorf("env:%s: not set or empty", name)
		}
		return v, nil

	case strings.HasPrefix(ref, "k8s:"):
		return resolveK8s(ctx, ref)

	case strings.HasPrefix(ref, "aws-kms:"):
		return resolveAWSKMS(ctx, ref)

	case strings.HasPrefix(ref, "aws-sm:"):
		return resolveAWSSM(ctx, ref)

	case strings.HasPrefix(ref, "vault:"):
		return resolveVault(ctx, ref)

	case strings.HasPrefix(ref, "keyring://"):
		return resolveKeyring(ctx, ref)

	case strings.HasPrefix(ref, "age:"), strings.HasPrefix(ref, "age-b64:"), strings.HasPrefix(ref, "age-file:"):
		return resolveAge(ctx, r.opts, ref)

	case strings.HasPrefix(ref, "secure:"):
		return resolveSecure(ctx, r.opts, ref)
	}

	return "", fmt.Errorf("unsupported secret ref: %q", ref)
}

func resolveK8s(ctx context.Context, ref string) (string, error) {
	r := strings.TrimSpace(ref[len("k8s:"):])
	parts := strings.Split(r, "/")
	if len(parts) != 3 {
		return "", fmt.Errorf("k8s ref must be k8s:namespace/secretName/dataKey")
	}
	ns, name, key := parts[0], parts[1], parts[2]
	if ns == "" || name == "" || key == "" {
		return "", fmt.Errorf("k8s: namespace, name, and key required")
	}
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, _ := os.UserHomeDir()
		kubeconfig = filepath.Join(home, ".kube", "config")
	}
	cc, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return "", fmt.Errorf("k8s kubeconfig: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(cc)
	if err != nil {
		return "", err
	}
	sec, err := clientset.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("k8s get secret %s/%s: %w", ns, name, err)
	}
	v, ok := sec.Data[key]
	if !ok {
		return "", fmt.Errorf("k8s: key %q not in secret %s/%s", key, ns, name)
	}
	return string(v), nil
}

func resolveAWSKMS(ctx context.Context, ref string) (string, error) {
	b64 := strings.TrimSpace(ref[len("aws-kms:"):])
	if b64 == "" {
		return "", fmt.Errorf("aws-kms: missing ciphertext")
	}
	blob, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("aws-kms: base64: %w", err)
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return "", err
	}
	svc := kms.NewFromConfig(cfg)
	out, err := svc.Decrypt(ctx, &kms.DecryptInput{CiphertextBlob: blob})
	if err != nil {
		return "", fmt.Errorf("aws-kms decrypt: %w", err)
	}
	return string(out.Plaintext), nil
}

func resolveAWSSM(ctx context.Context, ref string) (string, error) {
	secretID := strings.TrimSpace(ref[len("aws-sm:"):])
	if secretID == "" {
		return "", fmt.Errorf("aws-sm: missing secret id")
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return "", err
	}
	svc := secretsmanager.NewFromConfig(cfg)
	out, err := svc.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(secretID)})
	if err != nil {
		return "", fmt.Errorf("aws-sm:%s: %w", secretID, err)
	}
	if out.SecretString != nil {
		return *out.SecretString, nil
	}
	if len(out.SecretBinary) > 0 {
		return string(out.SecretBinary), nil
	}
	return "", fmt.Errorf("aws-sm:%s: empty secret", secretID)
}

func resolveVault(_ context.Context, ref string) (string, error) {
	r := strings.TrimSpace(ref[len("vault:"):])
	field := ""
	if i := strings.LastIndex(r, "#"); i >= 0 {
		field = strings.TrimSpace(r[i+1:])
		r = strings.TrimSpace(r[:i])
	}
	if r == "" {
		return "", fmt.Errorf("vault: missing path")
	}
	cfg := api.DefaultConfig()
	client, err := api.NewClient(cfg)
	if err != nil {
		return "", err
	}
	sec, err := client.Logical().Read(r)
	if err != nil {
		return "", fmt.Errorf("vault read %q: %w", r, err)
	}
	if sec == nil || sec.Data == nil {
		return "", fmt.Errorf("vault: no data at %q", r)
	}
	if inner, ok := sec.Data["data"].(map[string]any); ok {
		if field == "" {
			return "", fmt.Errorf("vault: field name required as path#field for KV v2 style response at %q", r)
		}
		v, ok := inner[field]
		if !ok {
			return "", fmt.Errorf("vault: field %q not found under %q", field, r)
		}
		s, _ := v.(string)
		if s == "" {
			return "", fmt.Errorf("vault: field %q is empty or not a string", field)
		}
		return s, nil
	}
	if field != "" {
		if v, ok := sec.Data[field]; ok {
			s, _ := v.(string)
			if s != "" {
				return s, nil
			}
		}
	}
	if field == "" {
		if v, ok := sec.Data["value"].(string); ok && v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("vault: could not resolve value from %q (use path#field for keyed secrets)", r)
}

func resolveKeyring(_ context.Context, ref string) (string, error) {
	rest := strings.TrimSpace(ref[len("keyring://"):])
	serviceName, user, ok := strings.Cut(rest, "/")
	serviceName, user = strings.TrimSpace(serviceName), strings.TrimSpace(user)
	if !ok || serviceName == "" || user == "" {
		return "", fmt.Errorf("keyring ref must be keyring://service/user")
	}
	v, err := keyring.Get(serviceName, user)
	if err != nil {
		return "", fmt.Errorf("keyring://%s/%s: %w", serviceName, user, err)
	}
	return v, nil
}

func resolveAge(ctx context.Context, opts Options, ref string) (string, error) {
	if len(opts.AgeIdentities) == 0 {
		return "", fmt.Errorf("age backends require identities (HONEY_AGE_IDENTITY_FILE or resolver options)")
	}
	switch {
	case strings.HasPrefix(ref, "age-b64:"):
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ref[len("age-b64:"):]))
		if err != nil {
			return "", fmt.Errorf("age-b64: decode: %w", err)
		}
		return decryptAgeBytes(opts.AgeIdentities, b)
	case strings.HasPrefix(ref, "age-file:"):
		rel := strings.TrimSpace(ref[len("age-file:"):])
		if rel == "" {
			return "", fmt.Errorf("age-file: missing path")
		}
		var base string
		if opts.RecipeDir != nil {
			base = opts.RecipeDir(ctx)
		}
		if base == "" {
			return "", fmt.Errorf("age-file: recipe directory unknown (internal error)")
		}
		abs := filepath.Clean(filepath.Join(base, rel))
		if !strings.HasPrefix(abs, filepath.Clean(base)+string(os.PathSeparator)) && abs != filepath.Clean(base) {
			return "", fmt.Errorf("age-file: path escapes recipe directory")
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			return "", fmt.Errorf("age-file: %w", err)
		}
		return decryptAgeBytes(opts.AgeIdentities, b)
	case strings.HasPrefix(ref, "age:"):
		payload := []byte(strings.TrimSpace(ref[len("age:"):]))
		if len(payload) == 0 {
			return "", fmt.Errorf("age: missing ciphertext")
		}
		return decryptAgeBytes(opts.AgeIdentities, payload)
	default:
		return "", fmt.Errorf("age: unsupported ref")
	}
}

func decryptAgeBytes(ids []age.Identity, armored []byte) (string, error) {
	r, err := age.Decrypt(bytes.NewReader(armored), ids...)
	if err != nil {
		return "", fmt.Errorf("age decrypt: %w", err)
	}
	var out strings.Builder
	if _, err := io.Copy(&out, r); err != nil {
		return "", err
	}
	return out.String(), nil
}

func resolveSecure(ctx context.Context, opts Options, ref string) (string, error) {
	return Unseal(ctx, opts, ref)
}
