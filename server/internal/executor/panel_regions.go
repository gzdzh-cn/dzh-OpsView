package executor

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"opsview/internal/hostfirewall"
	"opsview/internal/model"
	"opsview/internal/outbound"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type regionIndex struct {
	UpdatedAt string            `json:"updatedAt"`
	Prefixes  map[string]string `json:"prefixes"`
	Sources   []string          `json:"sources"`
}
type regionCache struct {
	stamp    string
	updated  string
	prefixes map[netip.Prefix]string
}

var regionsMu sync.Mutex
var regionsCache = map[string]regionCache{}

func (e *Engine) panelRegions(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	path := filepath.Join(e.Root, "client-auth", "regions.json")
	if op.Name == "system.panel-region-update" {
		e.runtimeMu.Lock()
		defer e.runtimeMu.Unlock()
		index := regionIndex{UpdatedAt: time.Now().UTC().Format(time.RFC3339), Prefixes: map[string]string{}, Sources: []string{"https://www.ipdeny.com/ipblocks/data/countries/all-zones.tar.gz", "https://www.ipdeny.com/ipv6/ipaddresses/blocks/ipv6-all-zones.tar.gz"}}
		for family, source := range index.Sources {
			req, _ := http.NewRequestWithContext(ctx, "GET", source, nil)
			resp, err := outbound.Client(60*time.Second, false).Do(req)
			if err != nil {
				return nil, errors.New("地区数据下载失败，保留原数据")
			}
			if resp.StatusCode != 200 {
				resp.Body.Close()
				return nil, errors.New("地区来源未返回成功结果")
			}
			gz, err := gzip.NewReader(io.LimitReader(resp.Body, 16<<20))
			if err != nil {
				resp.Body.Close()
				return nil, err
			}
			limited := &io.LimitedReader{R: gz, N: 80 << 20}
			tr := tar.NewReader(limited)
			total, count := int64(0), 0
			for {
				header, er := tr.Next()
				if er == io.EOF {
					break
				}
				if er != nil {
					gz.Close()
					resp.Body.Close()
					return nil, er
				}
				if header.Typeflag == tar.TypeDir {
					if header.Name == "." || header.Name == "./" {
						continue
					}
					return nil, errors.New("地区归档包含未知目录")
				}
				count++
				base := filepath.Base(header.Name)
				country := strings.ToUpper(strings.TrimSuffix(base, ".zone"))
				if header.Typeflag != tar.TypeReg || !regexp.MustCompile(`^[a-z]{2}\.zone$`).MatchString(base) || header.Size > 16<<20 || header.Size < 0 || count > 512 {
					gz.Close()
					resp.Body.Close()
					return nil, errors.New("地区归档格式无效")
				}
				total += header.Size
				if total > 64<<20 {
					gz.Close()
					resp.Body.Close()
					return nil, errors.New("地区数据超过大小限制")
				}
				scanner := bufio.NewScanner(tr)
				for scanner.Scan() {
					line := strings.TrimSpace(scanner.Text())
					if line == "" || strings.HasPrefix(line, "#") {
						continue
					}
					prefix, er := netip.ParsePrefix(line)
					if er != nil || prefix.Addr().Is4() != (family == 0) {
						gz.Close()
						resp.Body.Close()
						return nil, errors.New("地区数据 CIDR 或地址族无效")
					}
					index.Prefixes[prefix.Masked().String()] = country
					if len(index.Prefixes) > 1000000 {
						gz.Close()
						resp.Body.Close()
						return nil, errors.New("地区网段数量超过上限")
					}
				}
				if er = scanner.Err(); er != nil {
					gz.Close()
					resp.Body.Close()
					return nil, er
				}
			}
			_, er := io.Copy(io.Discard, limited)
			if er == nil && limited.N == 0 {
				er = errors.New("地区归档解压数据超过限制")
			}
			gz.Close()
			resp.Body.Close()
			if er != nil {
				return nil, er
			}
			if count < 100 {
				return nil, errors.New("地区数据不完整，保留当前数据")
			}
			log("已校验地区地址数据")
		}
		raw, _ := json.Marshal(index)
		if err := security.WriteAtomic(path, raw, 0600); err != nil {
			return nil, err
		}
		if err := security.WriteAtomic(filepath.Join(filepath.Dir(path), "IPdeny-Copyrights.txt"), []byte(hostfirewall.IPdenyCopyright), 0600); err != nil {
			return nil, err
		}
		return model.Config{"updatedAt": index.UpdatedAt, "prefixCount": len(index.Prefixes)}, nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return model.Config{"available": false, "reason": "请先更新本地地区地址数据"}, nil
	}
	stamp := st.ModTime().UTC().Format(time.RFC3339Nano)
	regionsMu.Lock()
	cached, ok := regionsCache[path]
	if !ok || cached.stamp != stamp {
		raw, er := os.ReadFile(path)
		if er != nil {
			regionsMu.Unlock()
			return nil, er
		}
		var index regionIndex
		if er = json.Unmarshal(raw, &index); er != nil {
			regionsMu.Unlock()
			return nil, er
		}
		cached = regionCache{stamp: stamp, updated: index.UpdatedAt, prefixes: map[netip.Prefix]string{}}
		for prefix, country := range index.Prefixes {
			p, er := netip.ParsePrefix(prefix)
			if er != nil {
				regionsMu.Unlock()
				return nil, er
			}
			cached.prefixes[p] = country
		}
		regionsCache[path] = cached
	}
	regionsMu.Unlock()
	result := model.Config{"available": true, "updatedAt": cached.updated, "prefixCount": len(cached.prefixes), "country": ""}
	ip, err := netip.ParseAddr(op.Params.String("ip"))
	if err != nil {
		return result, nil
	}
	ip = ip.Unmap()
	for bits := ip.BitLen(); bits >= 0; bits-- {
		prefix := netip.PrefixFrom(ip, bits).Masked()
		if country := cached.prefixes[prefix]; country != "" {
			result["country"] = country
			break
		}
	}
	return result, nil
}
