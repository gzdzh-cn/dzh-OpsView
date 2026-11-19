package dao

import (
	"opsview/internal/model"
	"opsview/internal/security"
	"time"
)

func (s *Store) Notification(rule, state string, c model.Config) error {
	_, err := s.DB.Exec(`INSERT INTO notification_events VALUES(?,?,?,?,?)`, security.ID(), rule, state, s.encode(c), Now())
	return err
}
func (s *Store) Notifications() ([]model.Config, error)        { return s.notificationRows(false) }
func (s *Store) PendingNotifications() ([]model.Config, error) { return s.notificationRows(true) }
func (s *Store) notificationRows(pending bool) ([]model.Config, error) {
	query := `SELECT id,rule_id,state,config,created_at FROM notification_events ORDER BY created_at DESC LIMIT 500`
	if pending {
		query = `SELECT id,rule_id,state,config,created_at FROM notification_events WHERE state='queued' ORDER BY rowid LIMIT 500`
	}
	rows, err := s.DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Config{}
	for rows.Next() {
		var id, rule, state, enc, created string
		if err = rows.Scan(&id, &rule, &state, &enc, &created); err != nil {
			return nil, err
		}
		v := model.Config{}
		if err = s.decode(enc, &v); err != nil {
			return nil, err
		}
		v["id"] = id
		v["ruleId"] = rule
		v["state"] = state
		v["createdAt"] = created
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) UpdateNotification(id, state string, c model.Config) error {
	_, err := s.DB.Exec(`UPDATE notification_events SET state=?,config=? WHERE id=?`, state, s.encode(c), id)
	return err
}
func (s *Store) QueueNotification(rule, channel, title, message string) error {
	return s.Notification(rule, "queued", model.Config{"channelId": channel, "title": title, "message": message, "attempts": 0, "nextAt": time.Now().Unix()})
}
