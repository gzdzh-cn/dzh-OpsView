package executor

import (
	"errors"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
)

func siteDocumentRoot(r model.Resource, base string) string {
	if root := r.Config.String("documentRoot"); root != "" {
		return root
	}
	return filepath.Join(base, "www")
}

// Existing executor records are authoritative. Browser metadata cannot redirect reads.
func (e *Engine) trustedSiteRoot(r model.Resource) (string, error) {
	stored, err := e.load(r.ID)
	if err != nil || stored.Kind != "site" {
		return "", errors.New("网站尚未部署")
	}
	root := siteDocumentRoot(stored, filepath.Join(e.Root, "sites", r.ID))
	if root != filepath.Join(e.Root, "sites", r.ID, "www") {
		if err := logic.ValidateSiteRootAt(root, stored.Config.String("siteBaseDirectory")); err != nil {
			return "", err
		}
	}
	if _, err := safePath("/", strings.TrimPrefix(root, "/")); err != nil {
		return "", err
	}
	return root, nil
}

func (e *Engine) prepareSiteRoot(r *model.Resource, base string) error {
	legacy := filepath.Join(base, "www")
	root := r.Config.String("rootDirectory")
	if stored, err := e.load(r.ID); err == nil {
		root = siteDocumentRoot(stored, base)
		r.Config["siteBaseDirectory"] = stored.Config.String("siteBaseDirectory")
	} else if _, err := os.Stat(legacy); err == nil && root == "" {
		root = legacy // unfinished installations created by older releases
	} else if r.Config.String("type") == "proxy" {
		root = legacy
	} else if root == "" {
		root = logic.DefaultSiteRoot(r.Config.String("domains"))
	}
	if root != legacy {
		if err := logic.ValidateSiteRootAt(root, r.Config.String("siteBaseDirectory")); err != nil {
			return err
		}
	}
	for _, other := range e.states() {
		if other.Kind == "site" && other.ID != r.ID && siteDocumentRoot(other, filepath.Join(e.Root, "sites", other.ID)) == root {
			return errors.New("根目录已属于另一个站点")
		}
	}
	if _, err := safePath("/", strings.TrimPrefix(root, "/")); err != nil {
		return err
	}
	if root != legacy {
		parents := []string{}
		for parent := filepath.Dir(root); parent != "/"; parent = filepath.Dir(parent) {
			parents = append([]string{parent}, parents...)
		}
		for _, parent := range parents {
			f, er := e.filePath(parent, false)
			if er != nil {
				return er
			}
			f.close()
			_, priorErr := os.Lstat(parent)
			if err := os.MkdirAll(parent, 0755); err != nil {
				return err
			}
			if os.IsNotExist(priorErr) {
				if err := os.Chmod(parent, 0755); err != nil {
					return err
				}
			}
			st, err := os.Lstat(parent)
			if err != nil {
				return err
			}
			uid, _, _ := fileOwner(st)
			if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0022 != 0 || uid != 0 {
				return errors.New("建站目录的上级路径必须由 root 所有且不可由其他用户写入")
			}
		}
		// Reservations survive failed deployments; a second site cannot adopt a pending project's files.
		claims, err := filepath.Glob(filepath.Join(e.Root, "state", "*.document-root"))
		if err != nil {
			return err
		}
		for _, p := range claims {
			if filepath.Base(p) == r.ID+".document-root" {
				continue
			}
			b, er := os.ReadFile(p)
			if er != nil {
				return er
			}
			if string(b) == root {
				return errors.New("根目录已被另一个站点部署占用，请先处理该站点")
			}
		}
		// Claim directory before creating content, allowing retries without taking over another project.
		claim := filepath.Join(e.Root, "state", r.ID+".document-root")
		prior, err := os.ReadFile(claim)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if len(prior) > 0 && string(prior) != root {
			return errors.New("部署已占用另一根目录，请保留原目录重试")
		}
		if len(prior) == 0 {
			entries, er := os.ReadDir(root)
			if er != nil && !os.IsNotExist(er) {
				return er
			}
			if len(entries) > 0 && !r.Config.Bool("adoptDirectory") {
				return errors.New("根目录非空，请选择新目录；不会自动接管已有项目")
			}
			if er = security.WriteAtomic(claim, []byte(root), 0600); er != nil {
				return er
			}
		}
	}
	_, priorRootErr := os.Lstat(root)
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	if os.IsNotExist(priorRootErr) {
		if err := os.Chmod(root, 0755); err != nil {
			return err
		}
	}
	// Parent hierarchy is not writable by site users; never follow a substituted link.
	if _, err := safePath("/", strings.TrimPrefix(root, "/")); err != nil {
		return err
	}
	r.Config["documentRoot"] = root
	return nil
}
