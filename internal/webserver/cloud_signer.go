package webserver

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/ui"
	"go.uber.org/zap"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/iamcredentials/v1"
	"google.golang.org/api/option"
)

const signedURLTTL = 15 * time.Minute

type cloudSigningHints struct {
	AWSProfile string
	AWSRegion  string
	GCPProject string
}

type signedTransferURLs struct {
	ObjectKey   string
	UploadURL   string
	DownloadURL string
	DeleteURL   string
}

type transferCredentialMaterial struct {
	Provider  string
	Env       map[string]string
	ExpiresAt time.Time
}

func transferObjectKey(cloud ui.AgentCloudBackend, src, dst hosts.Record) string {
	if strings.TrimSpace(cloud.Object) != "" {
		return strings.TrimSpace(cloud.Object)
	}
	prefix := strings.Trim(strings.TrimSpace(cloud.Prefix), "/")
	source := strings.ReplaceAll(strings.TrimSpace(src.Name), " ", "_")
	if source == "" {
		source = strings.ReplaceAll(strings.TrimSpace(src.PrimaryIP), " ", "_")
	}
	dest := strings.ReplaceAll(strings.TrimSpace(dst.Name), " ", "_")
	if dest == "" {
		dest = strings.ReplaceAll(strings.TrimSpace(dst.PrimaryIP), " ", "_")
	}
	if source == "" {
		source = "source"
	}
	if dest == "" {
		dest = "destination"
	}
	base := fmt.Sprintf("%s_to_%s_%d", source, dest, time.Now().UTC().UnixNano())
	if prefix == "" {
		return base
	}
	return prefix + "/" + base
}

func sanitizeSignedURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	u.RawQuery = ""
	return u.String()
}

func buildAWSignedURLs(ctx context.Context, cloud ui.AgentCloudBackend, key string, hints cloudSigningHints, keepObject bool) (signedTransferURLs, error) {
	region := strings.TrimSpace(cloud.Region)
	if region == "" {
		region = strings.TrimSpace(hints.AWSRegion)
	}
	if region == "" {
		region = "us-east-1"
	}
	loadOpts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}
	if profile := strings.TrimSpace(hints.AWSProfile); profile != "" {
		loadOpts = append(loadOpts, config.WithSharedConfigProfile(profile))
	}
	cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return signedTransferURLs{}, err
	}
	s3Opts := []func(*s3.Options){}
	if ep := strings.TrimSpace(cloud.Endpoint); ep != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(ep)
			o.UsePathStyle = true
		})
	}
	client := s3.NewFromConfig(cfg, s3Opts...)
	presign := s3.NewPresignClient(client)
	bucket := strings.TrimSpace(cloud.Bucket)
	put, err := presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(signedURLTTL))
	if err != nil {
		return signedTransferURLs{}, err
	}
	get, err := presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(signedURLTTL))
	if err != nil {
		return signedTransferURLs{}, err
	}
	out := signedTransferURLs{
		ObjectKey:   key,
		UploadURL:   put.URL,
		DownloadURL: get.URL,
	}
	if !keepObject {
		del, derr := presign.PresignDeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		}, s3.WithPresignExpires(signedURLTTL))
		if derr != nil {
			return signedTransferURLs{}, derr
		}
		out.DeleteURL = del.URL
	}
	return out, nil
}

func buildGCPSignedURLs(ctx context.Context, cloud ui.AgentCloudBackend, key string, hints cloudSigningHints, keepObject bool) (signedTransferURLs, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return signedTransferURLs{}, err
	}
	bh := client.Bucket(strings.TrimSpace(cloud.Bucket))
	putOpts := &storage.SignedURLOptions{
		Scheme:  storage.SigningSchemeV4,
		Method:  "PUT",
		Expires: time.Now().Add(signedURLTTL),
	}
	put, err := bh.SignedURL(key, putOpts)
	if err != nil {
		googleAccessID, signBytes, fallbackErr := resolveGCPSigningFallback(ctx, hints.GCPProject)
		if fallbackErr != nil {
			return signedTransferURLs{}, fmt.Errorf("%w; fallback signer resolution failed: %v", err, fallbackErr)
		}
		putOpts.GoogleAccessID = googleAccessID
		putOpts.SignBytes = signBytes
		put, err = bh.SignedURL(key, putOpts)
		if err != nil {
			return signedTransferURLs{}, err
		}
	}
	getOpts := &storage.SignedURLOptions{
		Scheme:  storage.SigningSchemeV4,
		Method:  "GET",
		Expires: time.Now().Add(signedURLTTL),
	}
	if putOpts.GoogleAccessID != "" {
		getOpts.GoogleAccessID = putOpts.GoogleAccessID
		getOpts.SignBytes = putOpts.SignBytes
	}
	get, err := bh.SignedURL(key, getOpts)
	if err != nil {
		return signedTransferURLs{}, err
	}
	out := signedTransferURLs{
		ObjectKey:   key,
		UploadURL:   put,
		DownloadURL: get,
	}
	if !keepObject {
		delOpts := &storage.SignedURLOptions{
			Scheme:  storage.SigningSchemeV4,
			Method:  "DELETE",
			Expires: time.Now().Add(signedURLTTL),
		}
		if putOpts.GoogleAccessID != "" {
			delOpts.GoogleAccessID = putOpts.GoogleAccessID
			delOpts.SignBytes = putOpts.SignBytes
		}
		del, derr := bh.SignedURL(key, delOpts)
		if derr != nil {
			return signedTransferURLs{}, derr
		}
		out.DeleteURL = del
	}
	return out, nil
}

func resolveGCPSigningFallback(ctx context.Context, gcpProject string) (string, func([]byte) ([]byte, error), error) {
	googleAccessID := strings.TrimSpace(os.Getenv("GOOGLE_ACCESS_ID"))
	if googleAccessID == "" {
		googleAccessID = strings.TrimSpace(os.Getenv("GOOGLE_SIGNER_SERVICE_ACCOUNT"))
	}
	if googleAccessID == "" {
		googleAccessID = strings.TrimSpace(os.Getenv("GOOGLE_IMPERSONATE_SERVICE_ACCOUNT"))
	}
	if googleAccessID == "" {
		if project := resolveGCPProjectHint(ctx, gcpProject); project != "" {
			email, derr := detectGCPDefaultComputeServiceAccount(ctx, project)
			if derr == nil {
				googleAccessID = strings.TrimSpace(email)
			}
		}
	}
	if googleAccessID == "" {
		if metadata.OnGCE() {
			email, err := metadata.Email("default")
			if err == nil {
				googleAccessID = strings.TrimSpace(email)
			}
		}
	}
	if googleAccessID == "" {
		return "", nil, fmt.Errorf("missing GOOGLE_ACCESS_ID/GOOGLE_SIGNER_SERVICE_ACCOUNT and unable to detect default service account for GCS signed URLs; set GOOGLE_ACCESS_ID to a signer service account email")
	}
	iamSvc, err := iamcredentials.NewService(ctx, option.WithScopes(iamcredentials.CloudPlatformScope))
	if err != nil {
		return "", nil, err
	}
	resource := "projects/-/serviceAccounts/" + googleAccessID
	signBytes := func(payload []byte) ([]byte, error) {
		resp, err := iamSvc.Projects.ServiceAccounts.SignBlob(resource, &iamcredentials.SignBlobRequest{
			Payload: base64.StdEncoding.EncodeToString(payload),
		}).Do()
		if err != nil {
			return nil, err
		}
		sig, decErr := base64.StdEncoding.DecodeString(resp.SignedBlob)
		if decErr != nil {
			return nil, decErr
		}
		return sig, nil
	}
	zap.L().Debug("using IAM SignBlob fallback for GCS signed URLs", zap.String("google_access_id", googleAccessID))
	return googleAccessID, signBytes, nil
}

func resolveGCPProjectHint(ctx context.Context, hint string) string {
	if p := strings.TrimSpace(hint); p != "" {
		return p
	}
	for _, k := range []string{"GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT"} {
		if p := strings.TrimSpace(os.Getenv(k)); p != "" {
			return p
		}
	}
	creds, err := google.FindDefaultCredentials(ctx, compute.CloudPlatformScope)
	if err == nil && strings.TrimSpace(creds.ProjectID) != "" {
		return strings.TrimSpace(creds.ProjectID)
	}
	return ""
}

func detectGCPDefaultComputeServiceAccount(ctx context.Context, project string) (string, error) {
	computeSvc, err := compute.NewService(ctx, option.WithScopes(compute.CloudPlatformScope))
	if err != nil {
		return "", err
	}
	proj, err := computeSvc.Projects.Get(strings.TrimSpace(project)).Do()
	if err != nil {
		return "", err
	}
	email := strings.TrimSpace(proj.DefaultServiceAccount)
	if email == "" {
		return "", fmt.Errorf("project %q has no default compute service account", project)
	}
	return email, nil
}

func (s *Server) buildSignedTransferURLs(ctx context.Context, cloud ui.AgentCloudBackend, src, dst hosts.Record, hints cloudSigningHints, keepObject bool) (signedTransferURLs, error) {
	key := transferObjectKey(cloud, src, dst)
	provider := normalizeTransferCloudProvider(cloud.Provider)
	var out signedTransferURLs
	var err error
	switch provider {
	case "s3":
		out, err = buildAWSignedURLs(ctx, cloud, key, hints, keepObject)
	case "googlecloudstorage":
		out, err = buildGCPSignedURLs(ctx, cloud, key, hints, keepObject)
	default:
		return signedTransferURLs{}, fmt.Errorf("unsupported cloud provider %q for signed-url transfer", cloud.Provider)
	}
	if err != nil {
		return signedTransferURLs{}, err
	}
	zap.L().Debug("generated signed transfer urls",
		zap.String("provider", provider),
		zap.String("bucket", strings.TrimSpace(cloud.Bucket)),
		zap.String("object_key", out.ObjectKey),
		zap.String("upload_url_base", sanitizeSignedURL(out.UploadURL)),
		zap.String("download_url_base", sanitizeSignedURL(out.DownloadURL)),
		zap.Bool("has_delete_url", strings.TrimSpace(out.DeleteURL) != ""),
	)
	return out, nil
}

func (s *Server) resolveTransferCredentialMaterial(ctx context.Context, cloud ui.AgentCloudBackend, hints cloudSigningHints) (transferCredentialMaterial, error) {
	provider := normalizeTransferCloudProvider(cloud.Provider)
	switch provider {
	case "s3":
		region := strings.TrimSpace(cloud.Region)
		if region == "" {
			region = strings.TrimSpace(hints.AWSRegion)
		}
		if region == "" {
			region = "us-east-1"
		}
		loadOpts := []func(*config.LoadOptions) error{
			config.WithRegion(region),
		}
		if profile := strings.TrimSpace(hints.AWSProfile); profile != "" {
			loadOpts = append(loadOpts, config.WithSharedConfigProfile(profile))
		}
		cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
		if err != nil {
			return transferCredentialMaterial{}, err
		}
		creds, err := cfg.Credentials.Retrieve(ctx)
		if err != nil {
			return transferCredentialMaterial{}, err
		}
		env := map[string]string{
			"AWS_ACCESS_KEY_ID":     strings.TrimSpace(creds.AccessKeyID),
			"AWS_SECRET_ACCESS_KEY": strings.TrimSpace(creds.SecretAccessKey),
			"AWS_SESSION_TOKEN":     strings.TrimSpace(creds.SessionToken),
			"AWS_REGION":            region,
		}
		return transferCredentialMaterial{
			Provider:  provider,
			Env:       env,
			ExpiresAt: creds.Expires,
		}, nil
	case "googlecloudstorage":
		tokenSource, err := google.DefaultTokenSource(ctx, storage.ScopeReadWrite)
		if err != nil {
			return transferCredentialMaterial{}, err
		}
		tok, err := tokenSource.Token()
		if err != nil {
			return transferCredentialMaterial{}, err
		}
		env := map[string]string{
			"GOOGLE_OAUTH_ACCESS_TOKEN": strings.TrimSpace(tok.AccessToken),
		}
		project := strings.TrimSpace(resolveGCPProjectHint(ctx, hints.GCPProject))
		if project != "" {
			env["GOOGLE_CLOUD_PROJECT"] = project
		}
		return transferCredentialMaterial{
			Provider:  provider,
			Env:       env,
			ExpiresAt: tok.Expiry,
		}, nil
	default:
		return transferCredentialMaterial{}, fmt.Errorf("unsupported cloud provider %q for credential resolution", cloud.Provider)
	}
}
