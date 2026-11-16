package security

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
)

func EncryptFile(v *Vault, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err = io.WriteString(out, "OVB1\n"); err != nil {
		return err
	}
	buf := make([]byte, 1<<20)
	i := 0
	for {
		n, re := in.Read(buf)
		if n > 0 {
			payload := append([]byte(strconv.Itoa(i)+":"), buf[:n]...)
			if _, err = io.WriteString(out, v.Encrypt(payload)+"\n"); err != nil {
				return err
			}
			i++
		}
		if re == io.EOF {
			break
		}
		if re != nil {
			return re
		}
	}
	_, err = io.WriteString(out, v.Encrypt([]byte("EOF:"+strconv.Itoa(i)))+"\n")
	if err != nil {
		return err
	}
	return out.Sync()
}
func DecryptFile(v *Vault, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 4096), 2<<20)
	if !scan.Scan() || scan.Text() != "OVB1" {
		return errors.New("备份格式无效")
	}
	i := 0
	end := false
	for scan.Scan() {
		if end {
			return errors.New("备份结束标记后存在数据")
		}
		b, err := v.Decrypt(scan.Text())
		if err != nil {
			return err
		}
		if string(b) == "EOF:"+strconv.Itoa(i) {
			end = true
			continue
		}
		prefix := strconv.Itoa(i) + ":"
		if !strings.HasPrefix(string(b), prefix) {
			return errors.New("备份块顺序无效")
		}
		if _, err = out.Write(b[len(prefix):]); err != nil {
			return err
		}
		i++
	}
	if err = scan.Err(); err != nil {
		return err
	}
	if !end {
		return errors.New("备份被截断")
	}
	return out.Sync()
}
