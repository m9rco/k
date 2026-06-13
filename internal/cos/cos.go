// Package cos publishes local asset files to Tencent Cloud Object Storage and
// returns their public URL. It exists so the image-to-video provider, which
// requires a publicly fetchable source-image URL, can work even when the studio
// itself runs on localhost/intranet.
package cos

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tencentyun/cos-go-sdk-v5"

	"gameasset/internal/config"
)

// Uploader puts bytes into a COS bucket under a base path and maps the stored
// object to a public URL via the configured CDN/custom-domain prefix.
type Uploader struct {
	client    *cos.Client
	basePath  string
	publicURL string
}

// New builds an Uploader from config. Returns (nil, nil) when COS is not
// configured so callers can treat the capability as simply unavailable.
func New(cfg config.COSConfig) (*Uploader, error) {
	if !cfg.Configured() {
		return nil, nil
	}
	// Bucket-level service URL: https://{bucket}.cos.{region}.myqcloud.com
	bucketURL := fmt.Sprintf("https://%s.cos.%s.myqcloud.com", cfg.Bucket, cfg.Region)
	u, err := url.Parse(bucketURL)
	if err != nil {
		return nil, fmt.Errorf("cos: bad bucket url: %w", err)
	}
	client := cos.NewClient(&cos.BaseURL{BucketURL: u}, &http.Client{
		Timeout: 60 * time.Second,
		Transport: &cos.AuthorizationTransport{
			SecretID:  cfg.SecretID,
			SecretKey: cfg.SecretKey,
		},
	})
	return &Uploader{
		client:    client,
		basePath:  strings.Trim(cfg.BasePath, "/"),
		publicURL: strings.TrimRight(cfg.PublicURLPrefix, "/"),
	}, nil
}

// Upload stores data under basePath/name and returns its public URL. contentType
// (e.g. "image/png") is set so the object serves correctly when fetched by URL.
func (u *Uploader) Upload(ctx context.Context, name string, data []byte, contentType string) (string, error) {
	key := name
	if u.basePath != "" {
		key = u.basePath + "/" + name
	}
	opt := &cos.ObjectPutOptions{
		ObjectPutHeaderOptions: &cos.ObjectPutHeaderOptions{ContentType: contentType},
	}
	_, err := u.client.Object.Put(ctx, key, bytes.NewReader(data), opt)
	if err != nil {
		return "", fmt.Errorf("cos: put %q: %w", key, err)
	}
	return u.publicURL + "/" + key, nil
}
