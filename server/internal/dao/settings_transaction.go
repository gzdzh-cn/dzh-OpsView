package dao

import (
	"database/sql"
	"errors"
	"reflect"
)

// MutateSetting commits a setting, session invalidation and audit as one unit.
// The callback must not access Store: it executes inside the database transaction.
func (s *Store) MutateSetting(key string, out any, create bool, action, ip string, change func() error) error {
	return s.mutateSetting(key, out, create, true, action, ip, change)
}

// PrepareSetting is used for a pending TOTP enrollment, before authentication changes.
func (s *Store) PrepareSetting(key string, out any, action, ip string, change func() error) error {
	return s.mutateSetting(key, out, false, false, action, ip, change)
}
func (s *Store) mutateSetting(key string, out any, create, invalidate bool, action, ip string, change func() error) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var encoded string
	err = tx.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&encoded)
	if create {
		if err == nil {
			return errors.New("管理员已初始化，拒绝覆盖")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	} else {
		if err != nil {
			return err
		}
		if err = s.decode(encoded, out); err != nil {
			return err
		}
	}
	if err = change(); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, s.encode(out)); err != nil {
		return err
	}
	if invalidate {
		if _, err = tx.Exec("DELETE FROM sessions"); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO audit(action,resource_id,ip,created_at) VALUES(?,'',?,?)`, action, ip, Now()); err != nil {
		return err
	}
	return tx.Commit()
}

// AddSession rejects credentials changed concurrently by a maintenance process.
func (s *Store) AddSession(expected any, hash, csrf string, expires int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var value string
	if err = tx.QueryRow("SELECT value FROM settings WHERE key='admin'").Scan(&value); err != nil {
		return err
	}
	current := reflect.New(reflect.TypeOf(expected))
	if err = s.decode(value, current.Interface()); err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, current.Elem().Interface()) {
		return errors.New("管理员已变更，请重新登录")
	}
	if _, err = tx.Exec("INSERT INTO sessions(hash,csrf,expires) VALUES(?,?,?)", hash, csrf, expires); err != nil {
		return err
	}
	return tx.Commit()
}
