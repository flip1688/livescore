// Package storage mirrors objects (currently team/league logos) into
// Cloudflare R2, an S3-compatible bucket, so we never hotlink thscore's CDN.
package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// ObjectStore is the seam the sync/service layer depends on so tests can
// substitute an in-memory fake instead of talking to R2.
type ObjectStore interface {
	Exists(ctx context.Context, key string) (bool, error)
	Put(ctx context.Context, key, contentType string, body []byte) error
	PublicURL(key string) string
}

// Config holds the R2 bucket coordinates. All fields are required unless
// noted otherwise.
type Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	PublicBaseURL   string
	// Endpoint overrides the derived R2 endpoint; used by tests to point at
	// an httptest server. Empty = "https://{AccountID}.r2.cloudflarestorage.com".
	Endpoint string
}

// R2 is a thin wrapper around the S3 client scoped to one bucket.
type R2 struct {
	client *s3.Client
	bucket string
	base   string
}

// New builds an R2 client. Region is fixed to "auto" and path-style
// addressing is forced on — both required by R2, and path-style also lets
// tests target a plain httptest server.
func New(ctx context.Context, cfg Config) (*R2, error) {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	return &R2{client: client, bucket: cfg.Bucket, base: cfg.PublicBaseURL}, nil
}

// Exists reports whether key is already present in the bucket.
func (r *R2) Exists(ctx context.Context, key string) (bool, error) {
	_, err := r.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}
	if isNotFound(err) {
		return false, nil
	}
	return false, err
}

// Put uploads body under key with the given content type.
func (r *R2) Put(ctx context.Context, key, contentType string, body []byte) error {
	_, err := r.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(r.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String(contentType),
	})
	return err
}

// PublicURL builds the URL clients should use to fetch key, via the CDN
// domain configured in PublicBaseURL rather than the R2 API endpoint.
func (r *R2) PublicURL(key string) string {
	return strings.TrimRight(r.base, "/") + "/" + key
}

// isNotFound treats both the typed S3 NotFound error and a bare 404 response
// (which is all a HEAD request against a non-AWS-compatible test server
// yields) as "object absent".
func isNotFound(err error) bool {
	var nf *types.NotFound
	if errors.As(err, &nf) {
		return true
	}
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) && respErr.HTTPStatusCode() == 404 {
		return true
	}
	return false
}
