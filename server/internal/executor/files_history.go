package executor

import (
	"errors"
	"io"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"time"
)

// Copy the prior text revision into the same-filesystem recycle bin so saving
// can still atomically rename the staged file over its destination.
func (e *Engine) retainTextRevision(path, revision string) error {
	source, err := e.openFile(path, false)
	if err != nil {
		return err
	}
	defer source.Close()
	st, err := source.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() > fileTextLimit || fileRevision(st) != revision {
		return &model.RevisionConflict{Revision: fileRevision(st)}
	}
	dir, err := e.trashDirectory(path)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	id := security.ID()
	out, err := root.OpenFile(id, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		out.Close()
		if !keep {
			_ = root.Remove(id)
		}
	}()
	if err = ensureFileSpace(out, st.Size()); err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(source, fileTextLimit+1))
	if err != nil {
		return err
	}
	after, err := source.Stat()
	if err != nil {
		return err
	}
	if n != st.Size() || fileRevision(after) != revision {
		return errors.New("保存期间文件发生变化，未覆盖")
	}
	uid, gid, _ := fileOwner(st)
	if err = out.Chown(uid, gid); err != nil {
		return err
	}
	if err = out.Chmod(st.Mode().Perm()); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	index, err := e.trashIndex()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	account, err := e.accountForPath(path)
	if err != nil {
		return err
	}
	index[id] = trashEntry{ID: id, Original: path, Stored: filepath.Join(dir, id), DeletedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour), Account: account, UID: uid, GID: gid}
	if err = e.writeFileMeta("trash", index); err != nil {
		return err
	}
	keep = true
	return nil
}
