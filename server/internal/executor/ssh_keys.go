package executor

import (
	"bytes"
	"context"
	"crypto/rsa"
	"errors"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Root's conventional authorized_keys is the only editable key file. No private
// key, arbitrary user-supplied path, AuthorizedKeysCommand or key options execute.
func (e *Engine) sshKeysFile(ctx context.Context) (string, error) {
	_, effective, err := e.sshEffective(ctx, e.sshPath())
	if err != nil {
		return "", err
	}
	allowed := false
	for _, line := range strings.Split(effective, "\n") {
		if strings.HasPrefix(line, "authorizedkeysfile ") {
			for _, p := range strings.Fields(line)[1:] {
				if p == ".ssh/authorized_keys" || p == "/root/.ssh/authorized_keys" || p == "%h/.ssh/authorized_keys" {
					allowed = true
				}
			}
		}
		if strings.HasPrefix(line, "authorizedkeyscommand ") && strings.TrimSpace(strings.TrimPrefix(line, "authorizedkeyscommand ")) != "none" {
			return "", errors.New("使用外部公钥命令，仅支持主机管理")
		}
	}
	if !allowed {
		return "", errors.New("自定义 AuthorizedKeysFile 仅提供诊断")
	}
	path := "/root/.ssh/authorized_keys"
	// Test configuration never accesses the developer's real keys.
	if e.sshConfigPath != "" {
		path = filepath.Join(filepath.Dir(e.sshConfigPath), ".ssh", "authorized_keys")
	}
	for _, p := range []string{filepath.Dir(filepath.Dir(path)), filepath.Dir(path), path} {
		st, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("公钥路径不能包含符号链接")
		}
	}
	return path, nil
}
func readSSHKeys(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		return []byte{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return nil, errors.New("公钥文件必须为不超过 1 MiB 的普通文件")
	}
	return os.ReadFile(path)
}
func (e *Engine) sshPublicKeys(ctx context.Context) model.Config {
	path, err := e.sshKeysFile(ctx)
	if err != nil {
		return model.Config{"editable": false, "reason": err.Error(), "rows": []model.Config{}}
	}
	b, err := readSSHKeys(path)
	if err != nil {
		return model.Config{"editable": false, "reason": err.Error(), "rows": []model.Config{}}
	}
	rows := []model.Config{}
	for _, line := range bytes.Split(b, []byte("\n")) {
		key, comment, options, _, err := ssh.ParseAuthorizedKey(line)
		if err == nil {
			rows = append(rows, model.Config{"fingerprint": ssh.FingerprintSHA256(key), "type": key.Type(), "comment": comment, "restricted": len(options) > 0})
		}
	}
	return model.Config{"editable": true, "rows": rows, "revision": security.Hash(string(b)), "scope": "root · authorized_keys（不包含其他账号、备用文件或外部密钥）"}
}
func (e *Engine) changeSSHPublicKey(ctx context.Context, op model.Operation) (model.Config, error) {
	if _, err := os.Stat(e.sshPendingPath()); !os.IsNotExist(err) {
		return nil, errors.New("SSH 设置待确认，暂不可修改公钥")
	}
	if _, err := os.Stat(e.sshServicePath()); !os.IsNotExist(err) {
		return nil, errors.New("SSH 服务停用待确认，暂不可修改公钥")
	}
	if !op.Params.Bool("confirmedImpact") {
		return nil, errors.New("请确认 root 公钥变更影响")
	}
	path, err := e.sshKeysFile(ctx)
	if err != nil {
		return nil, err
	}
	old, err := readSSHKeys(path)
	if err != nil {
		return nil, err
	}
	if security.Hash(string(old)) != op.Params.String("revision") {
		return nil, &model.RevisionConflict{Revision: security.Hash(string(old))}
	}
	var next []byte
	if op.Name == "system.ssh-key-add" {
		value := strings.TrimSpace(op.Params.String("publicKey"))
		if len(value) > 16384 {
			return nil, errors.New("公钥过长")
		}
		key, comment, options, rest, err := ssh.ParseAuthorizedKey([]byte(value))
		if err != nil || len(options) > 0 || len(bytes.TrimSpace(rest)) > 0 || strings.ContainsAny(value, "\r\n") {
			return nil, errors.New("请提供一行 OpenSSH 公钥，不支持私钥或自定义选项")
		}
		if key.Type() != "ssh-ed25519" && key.Type() != "ssh-rsa" && !strings.HasPrefix(key.Type(), "ecdsa-sha2-") {
			return nil, errors.New("公钥类型不受支持")
		}
		if cryptoKey, ok := key.(ssh.CryptoPublicKey); ok {
			if rsaKey, ok := cryptoKey.CryptoPublicKey().(*rsa.PublicKey); ok && rsaKey.N.BitLen() < 3072 {
				return nil, errors.New("RSA 公钥至少需要 3072 位")
			}
		}
		for _, line := range bytes.Split(old, []byte("\n")) {
			k, _, _, _, err := ssh.ParseAuthorizedKey(line)
			if err == nil && ssh.FingerprintSHA256(k) == ssh.FingerprintSHA256(key) {
				return nil, errors.New("该公钥已经存在")
			}
		}
		entry := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		if comment != "" {
			entry += " " + comment
		}
		next = append(append([]byte{}, old...), []byte("\n"+entry+"\n")...)
	} else {
		fingerprint := op.Params.String("fingerprint")
		if !strings.HasPrefix(fingerprint, "SHA256:") {
			return nil, errors.New("公钥指纹无效")
		}
		found, remaining := false, 0
		lines := []string{}
		for _, line := range strings.Split(string(old), "\n") {
			key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
			if err == nil && ssh.FingerprintSHA256(key) == fingerprint {
				found = true
				continue
			}
			if err == nil {
				remaining++
			}
			lines = append(lines, line)
		}
		if !found {
			return nil, errors.New("公钥不存在")
		}
		if remaining == 0 {
			return nil, errors.New("禁止删除最后一个 root 公钥，请先添加并验证替代公钥")
		}
		next = []byte(strings.Join(lines, "\n"))
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if err = security.WriteAtomic(path, next, 0600); err != nil {
		return nil, err
	}
	return model.Config{"updated": true}, nil
}
