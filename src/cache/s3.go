package cache

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pierrec/lz4/v4"
)

// S3Backend stores cache objects in an S3-compatible bucket with LZ4 compression.
type S3Backend struct {
	client *s3.Client
	bucket string
	prefix string
}

// NewS3Backend creates an S3 backend. It uses the default AWS credential chain
// (env vars, instance profile, etc.). Returns nil if bucket is empty.
func NewS3Backend(bucket, region, prefix string) (*S3Backend, error) {
	if bucket == "" {
		return nil, nil
	}
	if prefix == "" {
		prefix = "go-buildcache/"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	ctx := context.Background()
	var opts []func(*config.LoadOptions) error
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("s3 config: %w", err)
	}

	return &S3Backend{
		client: s3.NewFromConfig(cfg),
		bucket: bucket,
		prefix: prefix,
	}, nil
}

func (b *S3Backend) key(actionID string) string {
	return b.prefix + "v1" + actionID
}

// Get retrieves a cached object from S3. The returned body is decompressed.
func (b *S3Backend) Get(actionID string) (outputID string, body io.ReadCloser, size int64, t time.Time, miss bool, err error) {
	ctx := context.Background()
	resp, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &b.bucket,
		Key:    aws.String(b.key(actionID)),
	})
	if err != nil {
		// Any error (including NoSuchKey) is treated as a miss.
		return "", nil, 0, time.Time{}, true, nil
	}
	defer func() {
		if err != nil || miss {
			resp.Body.Close()
		}
	}()

	// Read the outputID from metadata.
	outputID = resp.Metadata["outputid"]
	if outputID == "" {
		return "", nil, 0, time.Time{}, true, nil
	}

	// Decompress the body.
	compressed, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", nil, 0, time.Time{}, true, nil
	}

	decompressed, err := decompressData(compressed)
	if err != nil {
		return "", nil, 0, time.Time{}, true, nil
	}

	t = time.Now()
	if resp.LastModified != nil {
		t = *resp.LastModified
	}

	return outputID, io.NopCloser(bytes.NewReader(decompressed)), int64(len(decompressed)), t, false, nil
}

// Put stores a cached object in S3 with LZ4 compression.
func (b *S3Backend) Put(actionID, outputID string, body io.Reader, bodySize int64) error {
	raw, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("s3 put read: %w", err)
	}

	compressed, err := compressData(raw)
	if err != nil {
		return fmt.Errorf("s3 put compress: %w", err)
	}

	ctx := context.Background()
	k := b.key(actionID)
	_, err = b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        &b.bucket,
		Key:           &k,
		Body:          bytes.NewReader(compressed),
		ContentLength: aws.Int64(int64(len(compressed))),
		Metadata: map[string]string{
			"outputid": outputID,
		},
	})
	return err
}

// Close is a no-op for S3.
func (b *S3Backend) Close() error { return nil }

func compressData(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := lz4.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decompressData(data []byte) ([]byte, error) {
	r := lz4.NewReader(bytes.NewReader(data))
	return io.ReadAll(r)
}
