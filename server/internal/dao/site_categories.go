package dao

import (
	"errors"
	"strings"
)

func (s *Store) SiteCategories() ([]string, error) {
	var saved []string
	_ = s.Setting("site-categories", &saved)
	rows, err := s.Resources("site")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{"默认分类": true}
	out := []string{"默认分类"}
	for _, name := range saved {
		if !seen[name] {
			out = append(out, name)
			seen[name] = true
		}
	}
	for _, r := range rows {
		if name := r.Config.String("category"); name != "" && !seen[name] {
			out = append(out, name)
			seen[name] = true
		}
	}
	return out, nil
}
func (s *Store) ChangeSiteCategory(old, name string, remove bool) error {
	name = strings.TrimSpace(name)
	if !remove && (name == "" || len(name) > 120 || strings.ContainsAny(name, "\x00\r\n")) {
		return errors.New("分类名称须为 1–40 个中文字符")
	}
	if old == "默认分类" {
		return errors.New("默认分类不可修改")
	}
	categories, err := s.SiteCategories()
	if err != nil {
		return err
	}
	rs, err := s.Resources("site")
	if err != nil {
		return err
	}
	next := []string{}
	for _, v := range categories {
		if v != old && v != name {
			next = append(next, v)
		}
	}
	if !remove {
		next = append(next, name)
	} else {
		name = "默认分类"
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if old != "" {
		for _, r := range rs {
			if r.Config.String("category") != old {
				continue
			}
			var active int
			if er := tx.QueryRow(`SELECT COUNT(*) FROM tasks WHERE resource_id=? AND state IN ('queued','running')`, r.ID).Scan(&active); er != nil {
				return er
			}
			if active > 0 {
				return errors.New("分类中的站点正在变更，请完成任务后重试")
			}
			r.Config["category"] = name
			result, er := tx.Exec(`UPDATE resources SET config=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`, s.encode(r.Config), Now(), r.ID, r.Revision)
			if er != nil {
				return er
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				return ErrRevision
			}
		}
	}
	if err = s.setTx(tx, "site-categories", next); err != nil {
		return err
	}
	return tx.Commit()
}
