package main

import (
	cloudstorage "cloud.google.com/go/storage"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	jose "github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

type transferArgs struct {
	Path        string
	Provider    string
	Bucket      string
	Object      string
	Region      string
	Endpoint    string
	CredsJWE    string
	KeyFile     string
	KeyDir      string
	ProbeAccess string
}

func require(v, name string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("missing --%s", name)
	}
	return nil
}

func readArgOrEnv(v, env string) string {
	if strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(os.Getenv(env))
}

func parseArgs(op string, argv []string) (transferArgs, error) {
	var a transferArgs
	fs0 := flag.NewFlagSet(op, flag.ContinueOnError)
	fs0.SetOutput(io.Discard)
	fs0.StringVar(&a.Path, "path", "", "path")
	fs0.StringVar(&a.Provider, "provider", "", "cloud provider")
	fs0.StringVar(&a.Bucket, "bucket", "", "cloud bucket")
	fs0.StringVar(&a.Object, "object", "", "cloud object")
	fs0.StringVar(&a.Region, "region", "", "cloud region")
	fs0.StringVar(&a.Endpoint, "endpoint", "", "cloud endpoint")
	fs0.StringVar(&a.CredsJWE, "creds_jwe", "", "credential jwe")
	fs0.StringVar(&a.KeyFile, "key_file", "", "private key file for jwe decryption")
	fs0.StringVar(&a.KeyDir, "key_dir", "", "directory for generated key files")
	fs0.StringVar(&a.ProbeAccess, "probe_access", "", "probe access mode (read|write)")
	if err := fs0.Parse(argv); err != nil {
		return a, err
	}
	a.Provider = readArgOrEnv(a.Provider, "HONEY_TRANSFER_PROVIDER")
	a.Bucket = readArgOrEnv(a.Bucket, "HONEY_TRANSFER_BUCKET")
	a.Object = readArgOrEnv(a.Object, "HONEY_TRANSFER_OBJECT")
	a.Region = readArgOrEnv(a.Region, "HONEY_TRANSFER_REGION")
	a.Endpoint = readArgOrEnv(a.Endpoint, "HONEY_TRANSFER_ENDPOINT")
	a.CredsJWE = readArgOrEnv(a.CredsJWE, "HONEY_TRANSFER_CREDS_JWE")
	a.KeyFile = readArgOrEnv(a.KeyFile, "HONEY_TRANSFER_KEY_FILE")
	a.KeyDir = readArgOrEnv(a.KeyDir, "HONEY_TRANSFER_KEY_DIR")
	a.Path = readArgOrEnv(a.Path, "HONEY_TRANSFER_PATH")
	a.ProbeAccess = readArgOrEnv(a.ProbeAccess, "HONEY_TRANSFER_PROBE_ACCESS")
	return a, nil
}

func normalizeProvider(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "gcs", "google", "google-cloud-storage":
		return "googlecloudstorage"
	default:
		return strings.ToLower(strings.TrimSpace(p))
	}
}

type keygenOutput struct {
	KID            string `json:"kid"`
	PublicJWK      string `json:"public_jwk"`
	PrivateKeyFile string `json:"private_key_file"`
}

func runKeygen(a transferArgs) error {
	dir := strings.TrimSpace(a.KeyDir)
	if dir == "" {
		dir = "/tmp"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	privDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	kidBytes := make([]byte, 12)
	if _, err := rand.Read(kidBytes); err != nil {
		return err
	}
	kid := hex.EncodeToString(kidBytes)
	keyFile := path.Join(dir, "honey-transfer-agent-"+kid+".pem")
	pemData := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER})
	if err := os.WriteFile(filepath.Clean(keyFile), pemData, 0o600); err != nil { // #nosec G304
		return err
	}
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
	out := keygenOutput{
		KID:            kid,
		PublicJWK:      string(pubRaw),
		PrivateKeyFile: keyFile,
	}
	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(out)
}

type credentialClaims struct {
	Iss      string            `json:"iss"`
	Aud      string            `json:"aud"`
	Exp      int64             `json:"exp"`
	Nbf      int64             `json:"nbf"`
	JTI      string            `json:"jti"`
	Scope    string            `json:"scope"`
	Provider string            `json:"provider"`
	Creds    map[string]string `json:"creds"`
}

func loadPrivateKey(pathValue string) (*ecdsa.PrivateKey, error) {
	if err := require(pathValue, "key_file"); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Clean(pathValue)) // #nosec G304
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("invalid pem private key")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return key, nil
}

func decryptClaims(a transferArgs) (*credentialClaims, error) {
	if err := require(a.CredsJWE, "creds_jwe"); err != nil {
		return nil, err
	}
	priv, err := loadPrivateKey(a.KeyFile)
	if err != nil {
		return nil, err
	}
	obj, err := jose.ParseEncrypted(
		strings.TrimSpace(a.CredsJWE),
		[]jose.KeyAlgorithm{jose.ECDH_ES},
		[]jose.ContentEncryption{jose.A256GCM},
	)
	if err != nil {
		return nil, err
	}
	plain, err := obj.Decrypt(priv)
	if err != nil {
		return nil, err
	}
	var claims credentialClaims
	if err := json.Unmarshal(plain, &claims); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Unix()
	if claims.Aud != "honey-transfer-agent" {
		return nil, fmt.Errorf("invalid jwe audience")
	}
	if claims.Exp > 0 && now > claims.Exp {
		return nil, fmt.Errorf("credential envelope expired")
	}
	if claims.Nbf > 0 && now < claims.Nbf {
		return nil, fmt.Errorf("credential envelope not active yet")
	}
	return &claims, nil
}

type cloudClient interface {
	ProbeRead(ctx context.Context, a transferArgs) error
	ProbeWrite(ctx context.Context, a transferArgs) error
	Upload(ctx context.Context, a transferArgs) error
	Download(ctx context.Context, a transferArgs) error
	Cleanup(ctx context.Context, a transferArgs) error
}

type awsClient struct {
	s3 *s3.Client
}

func newAWSClient(ctx context.Context, a transferArgs, claims *credentialClaims) (*awsClient, error) {
	creds := claims.Creds
	providerCreds := credentials.NewStaticCredentialsProvider(
		strings.TrimSpace(creds["AWS_ACCESS_KEY_ID"]),
		strings.TrimSpace(creds["AWS_SECRET_ACCESS_KEY"]),
		strings.TrimSpace(creds["AWS_SESSION_TOKEN"]),
	)
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(strings.TrimSpace(a.Region)),
		awsconfig.WithCredentialsProvider(providerCreds),
	)
	if err != nil {
		return nil, err
	}
	opts := []func(*s3.Options){}
	if ep := strings.TrimSpace(a.Endpoint); ep != "" {
		opts = append(opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(ep)
			o.UsePathStyle = true
		})
	}
	return &awsClient{s3: s3.NewFromConfig(cfg, opts...)}, nil
}

func (c *awsClient) ProbeRead(ctx context.Context, a transferArgs) error {
	_, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(a.Bucket), Key: aws.String(a.Object)})
	if err != nil && isAWSNotFound(err) {
		return nil
	}
	return err
}

func (c *awsClient) ProbeWrite(ctx context.Context, a transferArgs) error {
	key := strings.TrimSpace(a.Object) + ".probe"
	_, err := c.s3.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(a.Bucket), Key: aws.String(key), Body: strings.NewReader("probe")})
	if err != nil {
		return err
	}
	_, _ = c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(a.Bucket), Key: aws.String(key)})
	return nil
}

func (c *awsClient) Upload(ctx context.Context, a transferArgs) error {
	f, err := os.Open(filepath.Clean(a.Path)) // #nosec G304
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(a.Bucket),
		Key:    aws.String(a.Object),
		Body:   f,
	})
	return err
}

func (c *awsClient) Download(ctx context.Context, a transferArgs) error {
	out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(a.Bucket), Key: aws.String(a.Object)})
	if err != nil {
		return err
	}
	defer func() { _ = out.Body.Close() }()
	if err := os.MkdirAll(filepath.Dir(a.Path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Clean(a.Path), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640) // #nosec G304
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(f, out.Body)
	return err
}

func (c *awsClient) Cleanup(ctx context.Context, a transferArgs) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(a.Bucket), Key: aws.String(a.Object)})
	return err
}

type gcpClient struct {
	storage *cloudstorage.Client
}

func newGCPClient(ctx context.Context, a transferArgs, claims *credentialClaims) (*gcpClient, error) {
	token := strings.TrimSpace(claims.Creds["GOOGLE_OAUTH_ACCESS_TOKEN"])
	if token == "" {
		return nil, fmt.Errorf("missing GOOGLE_OAUTH_ACCESS_TOKEN in credential claims")
	}
	tokSrc := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token, TokenType: "Bearer"})
	opts := []option.ClientOption{option.WithTokenSource(tokSrc)}
	if ep := strings.TrimSpace(a.Endpoint); ep != "" {
		opts = append(opts, option.WithEndpoint(ep))
	}
	c, err := cloudstorage.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &gcpClient{storage: c}, nil
}

func (c *gcpClient) ProbeRead(ctx context.Context, a transferArgs) error {
	_, err := c.storage.Bucket(a.Bucket).Object(a.Object).Attrs(ctx)
	if err != nil && isGCPNotFound(err) {
		return nil
	}
	return err
}

func (c *gcpClient) ProbeWrite(ctx context.Context, a transferArgs) error {
	key := strings.TrimSpace(a.Object) + ".probe"
	obj := c.storage.Bucket(a.Bucket).Object(key)
	w := obj.NewWriter(ctx)
	if _, err := w.Write([]byte("probe")); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	_ = obj.Delete(ctx)
	return nil
}

func (c *gcpClient) Upload(ctx context.Context, a transferArgs) error {
	f, err := os.Open(filepath.Clean(a.Path)) // #nosec G304
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := c.storage.Bucket(a.Bucket).Object(a.Object).NewWriter(ctx)
	if _, err := io.Copy(w, f); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

func (c *gcpClient) Download(ctx context.Context, a transferArgs) error {
	r, err := c.storage.Bucket(a.Bucket).Object(a.Object).NewReader(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	if err := os.MkdirAll(filepath.Dir(a.Path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Clean(a.Path), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640) // #nosec G304
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(f, r)
	return err
}

func (c *gcpClient) Cleanup(ctx context.Context, a transferArgs) error {
	return c.storage.Bucket(a.Bucket).Object(a.Object).Delete(ctx)
}

func isAWSNotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := strings.TrimSpace(apiErr.ErrorCode())
		switch code {
		case "NotFound", "NoSuchKey", "404":
			return true
		}
	}
	return false
}

func isGCPNotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == 404
	}
	return false
}

func newCloudClient(ctx context.Context, a transferArgs, claims *credentialClaims) (cloudClient, error) {
	switch normalizeProvider(a.Provider) {
	case "s3":
		return newAWSClient(ctx, a, claims)
	case "googlecloudstorage":
		return newGCPClient(ctx, a, claims)
	default:
		return nil, fmt.Errorf("unsupported provider %q", a.Provider)
	}
}

func runUpload(ctx context.Context, a transferArgs) error {
	if err := require(a.Path, "path"); err != nil {
		return err
	}
	if err := require(a.Provider, "provider"); err != nil {
		return err
	}
	if err := require(a.Bucket, "bucket"); err != nil {
		return err
	}
	if err := require(a.Object, "object"); err != nil {
		return err
	}
	claims, err := decryptClaims(a)
	if err != nil {
		return err
	}
	client, err := newCloudClient(ctx, a, claims)
	if err != nil {
		return err
	}
	return client.Upload(ctx, a)
}

func runDownload(ctx context.Context, a transferArgs) error {
	if err := require(a.Path, "path"); err != nil {
		return err
	}
	if err := require(a.Provider, "provider"); err != nil {
		return err
	}
	if err := require(a.Bucket, "bucket"); err != nil {
		return err
	}
	if err := require(a.Object, "object"); err != nil {
		return err
	}
	claims, err := decryptClaims(a)
	if err != nil {
		return err
	}
	client, err := newCloudClient(ctx, a, claims)
	if err != nil {
		return err
	}
	return client.Download(ctx, a)
}

func runCleanup(ctx context.Context, a transferArgs) error {
	if err := require(a.Provider, "provider"); err != nil {
		return err
	}
	if err := require(a.Bucket, "bucket"); err != nil {
		return err
	}
	if err := require(a.Object, "object"); err != nil {
		return err
	}
	claims, err := decryptClaims(a)
	if err != nil {
		return err
	}
	client, err := newCloudClient(ctx, a, claims)
	if err != nil {
		return err
	}
	return client.Cleanup(ctx, a)
}

func runProbe(ctx context.Context, a transferArgs) error {
	if err := require(a.Provider, "provider"); err != nil {
		return err
	}
	if err := require(a.Bucket, "bucket"); err != nil {
		return err
	}
	if err := require(a.Object, "object"); err != nil {
		return err
	}
	claims, err := decryptClaims(a)
	if err != nil {
		return err
	}
	client, err := newCloudClient(ctx, a, claims)
	if err != nil {
		return err
	}
	access := strings.ToLower(strings.TrimSpace(a.ProbeAccess))
	if access == "" {
		access = "read"
	}
	switch access {
	case "write":
		return client.ProbeWrite(ctx, a)
	case "read":
		return client.ProbeRead(ctx, a)
	default:
		return fmt.Errorf("invalid --probe_access %q (expected read|write)", a.ProbeAccess)
	}
}

func runChecksum(a transferArgs) error {
	if err := require(a.Path, "path"); err != nil {
		return err
	}
	f, err := os.Open(filepath.Clean(a.Path)) // #nosec G304 -- path provided by caller.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(os.Stdout, hex.EncodeToString(h.Sum(nil)))
	return nil
}

func usage() {
	_, _ = fmt.Fprintf(os.Stderr, "usage: honey-transfer-agent <keygen|upload|download|cleanup|probe|checksum> [flags]\n")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	op := strings.TrimSpace(strings.ToLower(os.Args[1]))
	args, err := parseArgs(op, os.Args[2:])
	if err != nil {
		usage()
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(2)
	}
	ctx := context.Background()
	switch op {
	case "keygen":
		err = runKeygen(args)
	case "upload":
		err = runUpload(ctx, args)
	case "download":
		err = runDownload(ctx, args)
	case "cleanup":
		err = runCleanup(ctx, args)
	case "probe":
		err = runProbe(ctx, args)
	case "checksum":
		err = runChecksum(args)
	default:
		usage()
		err = fmt.Errorf("unknown op %q", op)
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
