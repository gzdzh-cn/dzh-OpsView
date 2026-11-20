package executor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const siteTemplateCSS = `:root{color-scheme:light dark;font-family:system-ui,-apple-system,"Segoe UI",sans-serif;color:#25334a;background:#f3f6fb}*{box-sizing:border-box}body{margin:0;min-height:100svh;display:grid;place-items:center;padding:24px}main{width:min(600px,100%);padding:48px;border:1px solid #e1e7f0;border-radius:20px;background:#fff;box-shadow:0 20px 70px #1d355710}.mark{display:grid;place-items:center;width:56px;height:56px;border-radius:16px;background:#e8f4ed;color:#218253;font-size:28px;font-weight:700}h1{font-size:clamp(25px,5vw,36px);margin:24px 0 12px;letter-spacing:-.03em}p{line-height:1.8;color:#65748a;margin:0 0 24px}a{display:inline-block;padding:10px 18px;border-radius:8px;color:white;background:#287b55;text-decoration:none}footer{border-top:1px solid #e8edf4;margin-top:32px;padding-top:18px;font-size:12px;color:#8793a5}@media(prefers-color-scheme:dark){:root{background:#131b27;color:#e0e8f4}main{background:#1c2737;border-color:#334157}p{color:#a9b7cc}footer{border-color:#334157;color:#8b9bb3}.mark{background:#203e33;color:#70d4a4}}@media(max-width:480px){main{padding:28px}}`

func defaultSitePage(notFound bool) string {
	title, text, mark, link := "站点已就绪", "网站已成功创建。请上传网站文件，开始构建你的内容。", "✓", ""
	if notFound {
		title, text, mark, link = "页面未找到", "你访问的页面可能已移动或不存在。请检查地址，或返回首页继续浏览。", "404", `<a href="/">返回首页</a>`
	}
	return fmt.Sprintf(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><title>%s</title><style>%s</style></head><body><main><div class="mark">%s</div><h1>%s</h1><p>%s</p>%s<footer>OpsView · 网站服务</footer></main></body></html>`, title, siteTemplateCSS, mark, title, text, link)
}
func createSiteTemplates(root string) error {
	scoped, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer scoped.Close()
	for _, name := range []string{"index.html", "404.html"} {
		f, er := scoped.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if errors.Is(er, os.ErrExist) {
			continue
		}
		if er != nil {
			return er
		}
		_, er = f.WriteString(defaultSitePage(name == "404.html"))
		if er == nil {
			er = f.Chmod(0644)
		}
		closeErr := f.Close()
		if er != nil {
			return er
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
func siteErrorPage(root string) string {
	return " error_page 404 /404.html;\n location = /404.html { root " + filepath.Clean(root) + "; internal; }\n"
}
