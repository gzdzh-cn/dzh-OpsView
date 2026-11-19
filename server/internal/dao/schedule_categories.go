package dao

import (
	"errors"
	"strings"
)

func (s *Store) ScheduleCategories() ([]string, error) {
	var saved []string
	_ = s.Setting("schedule-categories", &saved)
	rows, err := s.Resources("schedule")
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
func (s *Store) ChangeScheduleCategory(old, name string, remove bool) error {
	name = strings.TrimSpace(name)
	if !remove && (name == "" || len(name) > 120 || strings.ContainsAny(name, "\x00\r\n")) {
		return errors.New("分类名称须为 1–40 个中文字符")
	}
	if old == "默认分类" {
		return errors.New("默认分类不可修改")
	}
	categories, err := s.ScheduleCategories()
	if err != nil {
		return err
	}
	rs, err := s.Resources("schedule")
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
	if err = s.setTx(tx, "schedule-categories", next); err != nil {
		return err
	}
	return tx.Commit()
}
