// Package files wraps an S3-compatible object storage client. In local
// development this points at the dev S3 server (SeaweedFS); in production it MUST point at a
// managed S3-compatible bucket.
//
// The adapter is deliberately provider-neutral; feature code never imports
// the minio-go types directly, only the Client surface defined here.
// Storage is optional: New returns a nil *Client when S3_ENDPOINT is empty,
// and callers treat nil as "uploads unavailable".
package files

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/AGlElectronics/learn/backend/internal/config"
)

// Client wraps *minio.Client plus the configured bucket and an optional
// public-facing base URL used to rewrite presigned URLs for the browser
// (e.g. "s3:9000" → "localhost:9030" in Docker Compose).
type Client struct {
	mc            *minio.Client
	bucket        string
	publicBaseURL *url.URL
}

// New constructs a Client and ensures the configured bucket exists. It
// returns (nil, nil) when S3_ENDPOINT is not configured. The Learn default
// bucket is `learn-data-dev` for local dev — override in staging/prod with
// a managed bucket.
func New(ctx context.Context, cfg *config.Config) (*Client, error) {
	if !cfg.S3Enabled() {
		return nil, nil
	}
	endpointURL, err := url.Parse(cfg.S3Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse S3_ENDPOINT: %w", err)
	}
	host := endpointURL.Host
	if host == "" {
		host = endpointURL.Path
	}
	useSSL := endpointURL.Scheme == "https"

	bucketLookup := minio.BucketLookupAuto
	if cfg.S3UsePathStyle {
		bucketLookup = minio.BucketLookupPath
	}

	mc, err := minio.New(host, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.S3AccessKeyID, cfg.S3SecretAccessKey, ""),
		Secure:       useSSL,
		Region:       cfg.S3Region,
		BucketLookup: bucketLookup,
	})
	if err != nil {
		return nil, fmt.Errorf("create minio client: %w", err)
	}

	exists, err := mc.BucketExists(ctx, cfg.S3Bucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket: %w", err)
	}
	if !exists {
		if err := mc.MakeBucket(ctx, cfg.S3Bucket, minio.MakeBucketOptions{Region: cfg.S3Region}); err != nil {
			return nil, fmt.Errorf("create bucket: %w", err)
		}
	}

	publicBase, _ := publicBaseURL(cfg)

	return &Client{
		mc:            mc,
		bucket:        cfg.S3Bucket,
		publicBaseURL: publicBase,
	}, nil
}

func publicBaseURL(cfg *config.Config) (*url.URL, error) {
	if v := strings.TrimSpace(cfg.S3PublicBaseURL); v != "" {
		return url.Parse(v)
	}
	u, err := url.Parse(cfg.S3Endpoint)
	if err != nil {
		return nil, err
	}
	// Compose service hostnames are not reachable from the host browser;
	// swap the host "s3" (formerly "minio") for "localhost" so presigned URLs
	// returned to a dev SPA actually work. The S3_PUBLIC_BASE_URL override
	// above takes precedence.
	if h := u.Hostname(); h == "s3" || h == "minio" {
		u.Host = strings.Replace(u.Host, h, "localhost", 1)
	}
	return u, nil
}

// PresignedPut returns a URL the browser can PUT bytes to.
func (c *Client) PresignedPut(ctx context.Context, key string, expiry time.Duration) (string, error) {
	u, err := c.mc.PresignedPutObject(ctx, c.bucket, key, expiry)
	if err != nil {
		return "", fmt.Errorf("presign put: %w", err)
	}
	return c.rewriteURL(u).String(), nil
}

// PresignedGet returns a URL the browser can GET bytes from.
func (c *Client) PresignedGet(ctx context.Context, key string, expiry time.Duration) (string, error) {
	u, err := c.mc.PresignedGetObject(ctx, c.bucket, key, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("presign get: %w", err)
	}
	return c.rewriteURL(u).String(), nil
}

// PresignedDownload is PresignedGet with a download filename.
func (c *Client) PresignedDownload(ctx context.Context, key, filename string, expiry time.Duration) (string, error) {
	params := url.Values{}
	params.Set("response-content-disposition", `attachment; filename="`+strings.ReplaceAll(filename, `"`, "")+`"`)
	u, err := c.mc.PresignedGetObject(ctx, c.bucket, key, expiry, params)
	if err != nil {
		return "", fmt.Errorf("presign get: %w", err)
	}
	return c.rewriteURL(u).String(), nil
}

// PresignedPost returns a URL and form fields for a browser multipart POST
// straight to the bucket. Object storage itself rejects a different key,
// content type or a body larger than maxBytes, so the bytes never pass
// through (or get buffered by) the backend.
func (c *Client) PresignedPost(ctx context.Context, key, contentType string, maxBytes int64, expiry time.Duration) (string, map[string]string, error) {
	p := minio.NewPostPolicy()
	if err := p.SetBucket(c.bucket); err != nil {
		return "", nil, err
	}
	if err := p.SetKey(key); err != nil {
		return "", nil, err
	}
	if err := p.SetExpires(time.Now().UTC().Add(expiry)); err != nil {
		return "", nil, err
	}
	if err := p.SetContentType(contentType); err != nil {
		return "", nil, err
	}
	if err := p.SetContentLengthRange(1, maxBytes); err != nil {
		return "", nil, err
	}
	u, fields, err := c.mc.PresignedPostPolicy(ctx, p)
	if err != nil {
		return "", nil, fmt.Errorf("presign post: %w", err)
	}
	return c.rewriteURL(u).String(), fields, nil
}

// ObjectInfo is the subset of object metadata checked after an upload.
type ObjectInfo struct {
	Size        int64
	ContentType string
}

// Stat returns size and content type of an object.
func (c *Client) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	info, err := c.mc.StatObject(ctx, c.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("stat object: %w", err)
	}
	return ObjectInfo{Size: info.Size, ContentType: info.ContentType}, nil
}

// PutObject streams bytes into the bucket. Pass -1 as size if unknown; the
// SDK buffers in memory up to its default part size.
func (c *Client) PutObject(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := c.mc.PutObject(ctx, c.bucket, key, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("put object: %w", err)
	}
	return nil
}

// GetObject opens a streaming reader for the object. Caller must Close. A
// Stat probe runs first so a missing key surfaces as an error here rather
// than mid-stream where it is hard to turn into an HTTP 404.
func (c *Client) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := c.mc.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		return nil, fmt.Errorf("stat object: %w", err)
	}
	return obj, nil
}

// ReadHead returns up to the first n bytes of an object (for file-type
// checks).
func (c *Client) ReadHead(ctx context.Context, key string, n int64) ([]byte, error) {
	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(0, n-1); err != nil {
		return nil, err
	}
	obj, err := c.mc.GetObject(ctx, c.bucket, key, opts)
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	defer func() { _ = obj.Close() }()
	data, err := io.ReadAll(io.LimitReader(obj, n))
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	return data, nil
}

// Remove deletes an object. Called when a file metadata row is deleted.
func (c *Client) Remove(ctx context.Context, key string) error {
	return c.mc.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{})
}

// Bucket returns the configured bucket name (for health/log display).
func (c *Client) Bucket() string { return c.bucket }

func (c *Client) rewriteURL(u *url.URL) *url.URL {
	if c.publicBaseURL == nil {
		return u
	}
	out := *u
	out.Scheme = c.publicBaseURL.Scheme
	out.Host = c.publicBaseURL.Host
	return &out
}
