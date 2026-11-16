package model

// RevisionConflict survives the local executor transport without exposing
// configuration contents or credentials.
type RevisionConflict struct{ Revision string }

func (e *RevisionConflict) Error() string {
	return "配置已被其他操作或外部程序修改，请重新载入并比较差异"
}
