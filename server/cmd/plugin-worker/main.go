package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"net/url"
	"os"
	"time"
)

func check(config map[string]any) (map[string]any, error) {
	text := func(key string) string { v, _ := config[key].(string); return v }
	endpoint, err := url.Parse(text("endpoint"))
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || text("bucket") == "" {
		return nil, errors.New("请配置有效 HTTPS endpoint 和 bucket")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	region := text("region")
	if region == "" {
		region = "us-east-1"
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region), awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(text("accessKey"), text("secretKey"), "")))
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) { o.BaseEndpoint = aws.String(endpoint.String()); o.UsePathStyle = true })
	if _, err = client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(text("bucket"))}); err != nil {
		return nil, errors.New("S3 存储桶连接或权限检查失败")
	}
	return map[string]any{"status": "ok", "bucket": text("bucket"), "checkedAt": time.Now().UTC()}, nil
}
func main() {
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 1<<20)
	encoder := json.NewEncoder(os.Stdout)
	for scan.Scan() {
		var request struct {
			JSONRPC string `json:"jsonrpc"`
			ID      any    `json:"id"`
			Method  string `json:"method"`
			Params  struct {
				Config      map[string]any `json:"config"`
				Permissions []string       `json:"permissions"`
			} `json:"params"`
		}
		if err := json.Unmarshal(scan.Bytes(), &request); err != nil {
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "parse error"}})
			continue
		}
		reply := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "health":
			reply["result"] = map[string]any{"status": "ok", "protocol": "1.0", "capabilities": []string{"backup.s3"}}
		case "check":
			permitted := false
			outbound := false
			for _, permission := range request.Params.Permissions {
				if permission == "backup.s3" {
					permitted = true
				}
				if permission == "network.outbound" {
					outbound = true
				}
			}
			if !permitted || !outbound {
				reply["error"] = map[string]any{"code": -32604, "message": "缺少 backup.s3 或 network.outbound 权限"}
			} else if result, err := check(request.Params.Config); err != nil {
				reply["error"] = map[string]any{"code": -32000, "message": err.Error()}
			} else {
				reply["result"] = result
			}
		default:
			reply["error"] = map[string]any{"code": -32601, "message": "method not found"}
		}
		_ = encoder.Encode(reply)
	}
}
