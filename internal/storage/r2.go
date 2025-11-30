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

	endpoint := cfg.Endpoint
	if endpoint == "" {
		host := fmt.Sprintf("%s.r2.cloudflarestorage.com", cfg.AccountID)
		// 欧盟区域：<account_id>.eu.r2.cloudflarestorage.com
		if strings.EqualFold(region, "eu") || strings.EqualFold(region, "eu-auto") || strings.EqualFold(region, "europe") {
			host = fmt.Sprintf("%s.eu.r2.cloudflarestorage.com", cfg.AccountID)
		}
		endpoint = "https://" + host
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	publicBase := cfg.PublicBaseURL
	if publicBase == "" {
		publicBase = endpoint
	}

	return &R2Client{
		s3:            client,
		accountID:     cfg.AccountID,
		publicBaseURL: publicBase,
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

// PublicURL 生成公共访问 URL：
// 非欧盟：https://<account_id>.r2.cloudflarestorage.com/<bucket>/<key>
// 欧盟： https://<account_id>.eu.r2.cloudflarestorage.com/<bucket>/<key>
func (c *R2Client) PublicURL(bucket, key string) string {
	return fmt.Sprintf("%s/%s/%s", c.publicBaseURL, bucket, key)
}
