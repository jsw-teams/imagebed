package storage

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/jsw-teams/imagebed/internal/config"
)

// R2Client 封装 Cloudflare R2 的 S3 兼容客户端。
// publicBaseURL 现在从 endpoint 推导出来，不再依赖配置里的 public_base_url。
type R2Client struct {
	s3            *s3.Client
	accountID     string
	publicBaseURL string
}

func NewR2Client(ctx context.Context, cfg config.R2Config) (*R2Client, error) {
	region := cfg.Region
	if region == "" {
		region = "auto"
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	// 计算 endpoint：
	// 非欧盟：https://<account_id>.r2.cloudflarestorage.com
	// 欧盟： https://<account_id>.eu.r2.cloudflarestorage.com
	endpoint := cfg.Endpoint
	if endpoint == "" {
		host := fmt.Sprintf("%s.r2.cloudflarestorage.com", cfg.AccountID)
		if strings.EqualFold(region, "eu") ||
			strings.EqualFold(region, "eu-auto") ||
			strings.EqualFold(region, "europe") {
			host = fmt.Sprintf("%s.eu.r2.cloudflarestorage.com", cfg.AccountID)
		}
		endpoint = "https://" + host
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	return &R2Client{
		s3:            client,
		accountID:     cfg.AccountID,
		publicBaseURL: endpoint, // 直接用 endpoint 作为公共前缀
	}, nil
}

func (c *R2Client) CreateBucket(ctx context.Context, name string) error {
	_, err := c.s3.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(name),
	})
	return err
}

func (c *R2Client) DeleteBucket(ctx context.Context, name string) error {
	_, err := c.s3.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(name),
	})
	return err
}

func (c *R2Client) PutObject(
	ctx context.Context,
	bucket, key, contentType string,
	body io.Reader,
	size int64,
) error {
	_, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	})
	return err
}

// PublicURL 生成 R2 对象直链：
// 非欧盟：https://<account_id>.r2.cloudflarestorage.com/<bucket>/<key>
// 欧盟： https://<account_id>.eu.r2.cloudflarestorage.com/<bucket>/<key>
//
// 外部给用户看的链接已经改为 “当前域名 + /i/{id}”，所以这里只是调试 / 管理用。
func (c *R2Client) PublicURL(bucket, key string) string {
	return fmt.Sprintf("%s/%s/%s", c.publicBaseURL, bucket, key)
}