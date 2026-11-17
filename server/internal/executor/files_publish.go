package executor

import (
	"errors"
	"fmt"
	"os"
)

// Publish only after staging and validation. A failed rename restores a displaced
// destination; successful overwrites retain the previous entry in the recycle bin.
func (e *Engine) publishFileEntry(source *os.Root, name string, dst *managedFile, policy string) error {
	var displaced *trashEntry
	if _, err := dst.root.Lstat(dst.name); err == nil {
		if policy != "overwrite" {
			return errors.New("目标已存在，未覆盖")
		}
		item, err := e.trashFile(dst.path)
		if err != nil {
			return err
		}
		displaced = &item
	} else if !os.IsNotExist(err) {
		return err
	}
	err := renameFileEntry(source, name, dst.root, dst.name, true)
	if err != nil && displaced != nil {
		if _, rollback := e.fileTrash(map[string]any{"action": "restore", "id": displaced.ID}); rollback != nil {
			return fmt.Errorf("发布失败：%w；旧文件保留在回收站，恢复失败：%v", err, rollback)
		}
	}
	return err
}
