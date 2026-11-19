package dao

import (
	"database/sql"
	"encoding/json"
	"errors"
	"opsview/internal/model"
	"reflect"
	"time"
)

func (s *Store) patchTx(tx *sql.Tx, key string, revision int, patch model.Config, action, ip string) (model.Config, int, error) {
	rev := 0
	err := tx.QueryRow(`SELECT revision FROM setting_revisions WHERE key=?`, key).Scan(&rev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, 0, err
	}
	if rev != revision {
		return nil, rev, ErrRevision
	}
	value := model.Config{}
	var enc string
	err = tx.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&enc)
	if err == nil {
		if err = s.decode(enc, &value); err != nil {
			return nil, rev, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, rev, err
	}
	for k, v := range patch {
		value[k] = v
	}
	if err = s.setTx(tx, key, value); err != nil {
		return nil, rev, err
	}
	if _, err = tx.Exec(`INSERT INTO setting_revisions VALUES(?,?) ON CONFLICT(key) DO UPDATE SET revision=excluded.revision`, key, rev+1); err != nil {
		return nil, rev, err
	}
	if _, err = tx.Exec(`INSERT INTO audit(action,resource_id,ip,created_at) VALUES(?,?,?,?)`, action, key, ip, Now()); err != nil {
		return nil, rev, err
	}
	return value, rev + 1, nil
}
func (s *Store) setTx(tx *sql.Tx, key string, value any) error {
	_, err := tx.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, s.encode(value))
	return err
}

// StagePreferences stores rollback state and the new preferences in the same durable commit.
func (s *Store) StagePreferences(revision int, patch, rollback model.Config, tokenHash, ip string) (model.Config, int, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	if err = s.noPendingTx(tx); err != nil {
		return nil, 0, err
	}
	v, rev, err := s.patchTx(tx, "panel", revision, patch, "preferences.stage", ip)
	if err != nil {
		return nil, rev, err
	}
	if err = s.setTx(tx, "preference-pending", model.Config{"tokenHash": tokenHash, "rollback": rollback, "applied": patch, "deadline": time.Now().Add(120 * time.Second).Unix()}); err != nil {
		return nil, rev, err
	}
	return v, rev, tx.Commit()
}
func (s *Store) noPendingTx(tx *sql.Tx) error {
	var enc string
	err := tx.QueryRow(`SELECT value FROM settings WHERE key='preference-pending'`).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	pending := model.Config{}
	if err = s.decode(enc, &pending); err != nil {
		return err
	}
	if pending.Int("deadline") > 0 {
		return errors.New("有待确认的访问设置，请先确认或等待回退")
	}
	return nil
}
func (s *Store) ConfirmPreferences(hash string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var enc string
	if err = tx.QueryRow(`SELECT value FROM settings WHERE key='preference-pending'`).Scan(&enc); err != nil {
		return err
	}
	p := model.Config{}
	if err = s.decode(enc, &p); err != nil {
		return err
	}
	if p.String("tokenHash") != hash || p.Int("deadline") <= int(time.Now().Unix()) {
		return errors.New("访问确认已过期")
	}
	if _, err = tx.Exec(`DELETE FROM settings WHERE key='preference-pending'`); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) RollbackPreferences() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var enc string
	if err = tx.QueryRow(`SELECT value FROM settings WHERE key='preference-pending'`).Scan(&enc); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	p := model.Config{}
	if err = s.decode(enc, &p); err != nil {
		return err
	}
	if p.Int("deadline") == 0 || p.Int("deadline") > int(time.Now().Unix()) {
		return nil
	}
	current := model.Config{}
	if err = tx.QueryRow(`SELECT value FROM settings WHERE key='panel'`).Scan(&enc); err != nil {
		return err
	}
	if err = s.decode(enc, &current); err != nil {
		return err
	}
	var rollback, applied model.Config
	b, _ := json.Marshal(p["rollback"])
	_ = json.Unmarshal(b, &rollback)
	b, _ = json.Marshal(p["applied"])
	_ = json.Unmarshal(b, &applied)
	patch := model.Config{}
	conflicts := false
	for k, v := range rollback {
		old, _ := json.Marshal(current[k])
		next, _ := json.Marshal(applied[k])
		if reflect.DeepEqual(old, next) {
			patch[k] = v
		} else {
			conflicts = true
		}
	}
	var rev int
	_ = tx.QueryRow(`SELECT revision FROM setting_revisions WHERE key='panel'`).Scan(&rev)
	if _, _, err = s.patchTx(tx, "panel", rev, patch, "preferences.timeout-rollback", "local"); err != nil {
		return err
	}
	if conflicts {
		if _, err = tx.Exec(`INSERT INTO audit(action,resource_id,ip,created_at) VALUES('preferences.rollback-conflict','panel','local',?)`, Now()); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DELETE FROM settings WHERE key='preference-pending'`); err != nil {
		return err
	}
	return tx.Commit()
}
