package executor

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"io"
	"io/fs"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) backup(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.backupUnlocked(ctx, op, log)
}
func (e *Engine) backupUnlocked(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	r := op.Resource
	action := strings.TrimPrefix(op.Name, "backup.")
	if stored, err := e.load(r.ID); err == nil && stored.Config.Bool("recycleOnly") {
		r = stored
		op.Resource = stored
	}
	if r.Config.Bool("recycleOnly") && action != "backup" {
		return nil, errors.New("回收备份须通过数据库回收站操作")
	}
	if action == "delete" {
		if _, err := e.load(r.ID); err != nil {
			return nil, err
		}
		log("备份任务已移除，已有备份文件保留")
		return model.Config{}, e.forget(r.ID)
	}
	target, err := e.load(r.Config.String("targetId"))
	if err != nil {
		return nil, err
	}
	if target.Status == "recycled" {
		return nil, errors.New("已回收数据库仅能通过回收站恢复")
	}
	if action == "restore" {
		return e.restore(ctx, op, target, log)
	}
	if action != "backup" {
		return nil, errors.New("备份操作无效")
	}
	base := filepath.Join(e.Root, "backups", r.ID)
	if err = os.MkdirAll(base, 0700); err != nil {
		return nil, err
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000")
	plain := filepath.Join(base, stamp+".tar.gz")
	defer os.Remove(plain)
	if target.Kind == "instance" {
		if err = e.dumpRedis(ctx, target, plain, log); err != nil {
			return nil, err
		}
	} else if target.Kind == "database" {
		inst, err := e.load(target.Config.String("instanceId"))
		if err != nil {
			return nil, err
		}
		if err = e.dumpDatabase(ctx, inst, target, plain, log); err != nil {
			return nil, err
		}
	} else {
		folder := "sites"
		suffix := "www"
		if target.Kind == "app" {
			folder = "apps"
			suffix = ""
		}
		root := filepath.Join(e.Root, folder, target.ID, suffix)
		if target.Kind == "site" {
			root, err = e.trustedSiteRoot(target)
			if err != nil {
				return nil, err
			}
		}
		log("文件备份采用在线文件快照，不保证应用事务一致性")
		if err = archiveDirectory(root, plain); err != nil {
			return nil, err
		}
	}
	vault, err := security.OpenVault(filepath.Join(e.Root, "backup.key"))
	if err != nil {
		return nil, err
	}
	artifact := filepath.Join(base, stamp+".ovb")
	if err = encryptBackup(vault, plain, artifact); err != nil {
		return nil, err
	}
	hash, err := hashFile(artifact)
	if err != nil {
		return nil, err
	}
	stat, err := os.Stat(artifact)
	if err != nil {
		return nil, err
	}
	manifest := model.Config{"file": filepath.Base(artifact), "sha256": hash, "size": stat.Size(), "createdAt": time.Now().UTC().Format(time.RFC3339), "targetId": target.ID, "targetKind": target.Kind, "consistency": "online-file-copy"}
	if target.Kind == "database" {
		manifest["consistency"] = "logical-dump"
	}
	if target.Kind == "instance" {
		manifest["consistency"] = "redis-replication-rdb"
		manifest["software"] = target.Config.String("software")
		manifest["version"] = target.Config.String("version")
	}
	mb, _ := json.Marshal(manifest)
	if err = security.WriteAtomic(artifact+".json", mb, 0600); err != nil {
		return nil, err
	}
	if r.Config.String("destination") == "s3" {
		if err = uploadS3(ctx, r.Config, artifact, r.ID+"/"+filepath.Base(artifact)); err != nil {
			return nil, err
		}
		if err = uploadS3(ctx, r.Config, artifact+".json", r.ID+"/"+filepath.Base(artifact)+".json"); err != nil {
			return nil, err
		}
	}
	r.Config["artifact"] = filepath.Base(artifact)
	r.Config["sha256"] = hash
	r.Config["lastBackupAt"] = manifest["createdAt"]
	keep := r.Config.Int("retention")
	if keep < 1 {
		keep = 7
	}
	if keep > 100 {
		keep = 100
	}
	files, _ := filepath.Glob(base + "/*.ovb")
	for len(files) > keep {
		old := files[0]
		if r.Config.String("destination") == "s3" {
			if err = deleteS3(ctx, r.Config, r.ID+"/"+filepath.Base(old)); err != nil {
				return nil, err
			}
			_ = deleteS3(ctx, r.Config, r.ID+"/"+filepath.Base(old)+".json")
		}
		_ = os.Remove(old)
		_ = os.Remove(old + ".json")
		files = files[1:]
	}
	r.Config["history"] = backupHistory(base)
	if err = e.save(r); err != nil {
		return nil, err
	}
	log("加密备份及 SHA-256 校验完成")
	return model.Config{"artifact": r.Config["artifact"], "sha256": hash, "lastBackupAt": r.Config["lastBackupAt"], "history": r.Config["history"]}, nil
}
func backupHistory(base string) []model.Config {
	paths, _ := filepath.Glob(base + "/*.ovb.json")
	out := []model.Config{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m model.Config
		if json.Unmarshal(b, &m) == nil {
			out = append(out, m)
		}
	}
	return out
}
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	_, err = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), err
}
func encryptBackup(v *security.Vault, src, dst string) error {
	return security.EncryptFile(v, src, dst)
}
func decryptBackup(v *security.Vault, src, dst string) error {
	return security.DecryptFile(v, src, dst)
}
func archiveDirectory(root, path string) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)

	scoped, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer scoped.Close()
	err = fs.WalkDir(scoped.FS(), ".", func(rel string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if rel == "." {
			return nil
		}
		st, err := scoped.Lstat(rel)
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 || (!st.IsDir() && !st.Mode().IsRegular()) {
			return errors.New("备份目录含符号链接或特殊文件，请移除后重试")
		}
		var in *os.File
		if st.Mode().IsRegular() {
			in, err = scoped.Open(rel)
			if err != nil {
				return err
			}
			defer in.Close()
			st, err = in.Stat()
			if err != nil {
				return err
			}
			if !st.Mode().IsRegular() {
				return errors.New("备份期间文件类型发生变化")
			}
		}
		h, err := tar.FileInfoHeader(st, "")
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		if err = tw.WriteHeader(h); err != nil {
			return err
		}
		if in != nil {
			_, err = io.CopyN(tw, in, st.Size())
		}
		return err
	})
	ce := tw.Close()
	ge := gz.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	return ge
}
func (e *Engine) dumpDatabase(ctx context.Context, inst, target model.Resource, path string, log func(string)) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	binary := "mysqldump"
	args := []string{"--protocol=TCP", "-h127.0.0.1", "-P" + strconv.Itoa(inst.Config.Int("port")), "-uroot", "--single-transaction", "--routines", "--events", "--triggers", "--set-gtid-purged=OFF", "--", target.Name}
	env := []string{"MYSQL_PWD=" + inst.Config.String("password")}
	if inst.Config.String("software") == "postgresql" {
		binary = "pg_dump"
		args = []string{"-h", "127.0.0.1", "-p", strconv.Itoa(inst.Config.Int("port")), "-U", "postgres", "-Fc", target.Name}
		env = []string{"PGPASSWORD=" + inst.Config.String("password")}
	}
	var cmd *exec.Cmd
	if inst.Config.String("mode") == "docker" {
		all := append([]string{"exec", "-e", strings.SplitN(env[0], "=", 2)[0], "opsview-" + inst.ID, binary}, args...)
		cmd = exec.CommandContext(ctx, "docker", all...)
		cmd.Env = append(os.Environ(), env...)
	} else {
		cmd = exec.CommandContext(ctx, filepath.Join(e.Root, "instances", inst.ID, "runtime", "bin", binary), args...)
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdout = out
	cmd.Stderr = &logWriter{fn: func(string) {}}
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("数据库导出失败：%w", err)
	}
	if inst.Config.String("software") == "postgresql" {
		rolesPath := path + ".roles.sql"
		defer os.Remove(rolesPath)
		globals := []string{"-h", "127.0.0.1", "-p", strconv.Itoa(inst.Config.Int("port")), "-U", "postgres", "--roles-only"}
		var rolesCmd *exec.Cmd
		if inst.Config.String("mode") == "docker" {
			rolesCmd = exec.CommandContext(ctx, "docker", append([]string{"exec", "-e", strings.SplitN(env[0], "=", 2)[0], "opsview-" + inst.ID, "pg_dumpall"}, globals...)...)
			rolesCmd.Env = append(os.Environ(), env...)
		} else {
			rolesCmd = exec.CommandContext(ctx, filepath.Join(e.Root, "instances", inst.ID, "runtime/bin/pg_dumpall"), globals...)
			rolesCmd.Env = append(os.Environ(), env...)
		}
		roles, err := rolesCmd.Output()
		if err != nil {
			return err
		}
		if err = security.WriteAtomic(rolesPath, roles, 0600); err != nil {
			return err
		}
		out.Close()
		tmp := path + ".dump"
		if err = os.Rename(path, tmp); err != nil {
			return err
		}
		defer os.Remove(tmp)
		stage, err := os.MkdirTemp(filepath.Dir(path), "pg-backup-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(stage)
		if err = os.Rename(tmp, stage+"/database.dump"); err != nil {
			return err
		}
		if err = os.Rename(rolesPath, stage+"/roles.sql"); err != nil {
			return err
		}
		return archiveDirectory(stage, path)
	}
	return nil
}
func (e *Engine) restore(ctx context.Context, op model.Operation, target model.Resource, log func(string)) (model.Config, error) {
	r, err := e.load(op.Resource.ID)
	if err != nil {
		return nil, err
	}
	name := op.Params.String("artifact")
	if name == "" {
		name = r.Config.String("artifact")
	}
	if filepath.Base(name) != name || !strings.HasSuffix(name, ".ovb") {
		return nil, errors.New("备份文件名无效")
	}
	base := filepath.Join(e.Root, "backups", r.ID)
	artifact := filepath.Join(base, name)

	// Metadata in encrypted executor state is the trust anchor, never a remote sidecar.
	var history []model.Config
	raw, _ := json.Marshal(r.Config["history"])
	_ = json.Unmarshal(raw, &history)
	var manifest model.Config
	for _, item := range history {
		if item.String("file") == name {
			manifest = item
			break
		}
	}
	if manifest == nil || manifest.String("targetId") != target.ID || manifest.String("targetKind") != target.Kind || len(manifest.String("sha256")) != 64 {
		return nil, errors.New("备份不在受信任历史中或目标不匹配")
	}
	if _, err = os.Stat(artifact); os.IsNotExist(err) && r.Config.String("destination") == "s3" {
		log("从 S3 取回备份，使用本机加密历史校验摘要")
		if err = downloadS3(ctx, r.Config, artifact, r.ID+"/"+name, manifest); err != nil {
			return nil, err
		}
	}
	hash, err := hashFile(artifact)
	if err != nil {
		return nil, err
	}
	if hash != manifest.String("sha256") {
		return nil, errors.New("备份校验失败")
	}

	v, err := security.OpenVault(filepath.Join(e.Root, "backup.key"))
	if err != nil {
		return nil, err
	}
	plain := filepath.Join(base, "restore-"+security.ID()+".tar.gz")
	defer os.Remove(plain)
	if err = decryptBackup(v, artifact, plain); err != nil {
		return nil, err
	}
	if target.Kind == "database" {
		return e.restoreDatabase(ctx, op, target, plain, log)
	}
	if target.Kind == "instance" {
		return e.restoreRedis(ctx, op, target, plain, log)
	}
	dest := filepath.Join(e.Root, "restores", target.ID+"-"+security.ID())
	if err = os.MkdirAll(dest, 0750); err != nil {
		return nil, err
	}
	if err = extractArchive(ctx, plain, "restore.tar.gz", dest, false); err != nil {
		return nil, err
	}
	log("备份已恢复到新目录，原站点未覆盖：" + dest)
	return model.Config{"restorePath": dest}, nil
}
func (e *Engine) restoreDatabase(ctx context.Context, op model.Operation, target model.Resource, path string, log func(string)) (model.Config, error) {
	name := op.Params.String("databaseName")
	if !logicSQLName(name) || name == target.Name {
		return nil, errors.New("请提供新的数据库名，禁止覆盖源数据库")
	}
	inst, err := e.load(target.Config.String("instanceId"))
	if err != nil {
		return nil, err
	}

	recovered := model.Resource{ID: security.ID(), Kind: "database", Name: name, Config: mapClone(target.Config)}
	recovered.Config["username"] = name
	recovered.Config["password"] = security.ID() + security.ID()
	recovered.Config["imported"] = false
	recovered.Config["owner"] = name
	recovered.Config["managedAccount"] = true
	for _, key := range []string{"recyclePhase", "recycleBackupId", "expiresAt"} {
		delete(recovered.Config, key)
	}
	if targetID := op.Params.String("targetInstanceId"); targetID != "" && targetID != inst.ID {
		next, er := e.load(targetID)
		if er != nil || next.Kind != "instance" || next.Config.String("software") != inst.Config.String("software") {
			return nil, errors.New("恢复目标须为同引擎实例")
		}
		if databaseBranch(next) != databaseBranch(inst) {
			return nil, errors.New("跨大版本恢复请使用已验证的迁移流程")
		}
		inst = next
		recovered.Config["instanceId"] = inst.ID
	}
	recovered.Config["source"] = "127.0.0.1"
	recovered.Config["allowRemote"] = false
	if _, err = e.database(ctx, model.Operation{Name: "database.create", Resource: recovered, Params: model.Config{}}, log); err != nil {
		return nil, err
	}
	recovered.Status = "restoring"
	if err = e.save(recovered); err != nil {
		return nil, err
	}
	if inst.Config.String("software") == "mysql" {
		in, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer in.Close() // Dump CREATE/USE statements must not overwrite the source database.
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 4096), 32<<20)
		var filtered strings.Builder
		for scanner.Scan() {
			s := scanner.Text()
			if strings.HasPrefix(s, "CREATE DATABASE ") || strings.HasPrefix(s, "USE `") {
				continue
			}
			filtered.WriteString(s + "\n")
			if filtered.Len() > 256<<20 {
				return nil, errors.New("SQL 备份超过首版恢复限制 256 MiB")
			}
		}
		if err = scanner.Err(); err != nil {
			return nil, err
		}
		if err = e.sql(ctx, inst, name, strings.NewReader(filtered.String()), func(string) {}); err != nil {
			return nil, err
		}
	} else {
		stage, err := os.MkdirTemp(filepath.Dir(path), "pg-restore-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(stage)
		if err = extractArchive(ctx, path, "restore.tar.gz", stage, false); err != nil {
			return nil, err
		}
		if err = e.pgRestore(ctx, inst, recovered, stage+"/database.dump", log); err != nil {
			return nil, err
		}

	}
	log("已恢复到新数据库 " + name + "，请验证后切换连接")
	recovered.Status = "ready"
	recovered.Config["engine"] = inst.Config.String("software")
	if err = e.save(recovered); err != nil {
		return nil, err
	}
	return model.Config{"restoredDatabase": name, "linkedDatabase": recovered}, nil
}
func logicSQLName(s string) bool {
	if len(s) == 0 || len(s) > 63 {
		return false
	}
	for i, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && (r >= '0' && r <= '9' || r == '_')) {
			return false
		}
	}
	return true
}
func s3Client(ctx context.Context, c model.Config) (*s3.Client, error) {
	region := c.String("region")
	if region == "" {
		region = "us-east-1"
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region), awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.String("accessKey"), c.String("secretKey"), "")))
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) { o.BaseEndpoint = aws.String(c.String("endpoint")); o.UsePathStyle = true }), nil
}
func uploadS3(ctx context.Context, c model.Config, path, key string) error {
	client, err := s3Client(ctx, c)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(c.String("bucket")), Key: aws.String(key), Body: f})
	return err
}
func deleteS3(ctx context.Context, c model.Config, key string) error {
	client, err := s3Client(ctx, c)
	if err != nil {
		return err
	}
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(c.String("bucket")), Key: aws.String(key)})
	return err
}

func downloadS3(ctx context.Context, c model.Config, path, key string, manifest model.Config) error {
	client, err := s3Client(ctx, c)
	if err != nil {
		return err
	}
	response, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(c.String("bucket")), Key: aws.String(key)})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	expected := int64(manifest.Int("size"))
	if expected < 1 {
		return errors.New("备份大小元数据无效")
	}
	if response.ContentLength != nil && *response.ContentLength != expected {
		return errors.New("S3 备份大小不匹配")
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(path), "s3-restore-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	hash := sha256.New()
	count, err := io.Copy(io.MultiWriter(out, hash), io.LimitReader(response.Body, expected+1))
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if count != expected || hex.EncodeToString(hash.Sum(nil)) != manifest.String("sha256") {
		return errors.New("S3 备份完整性校验失败")
	}
	return os.Rename(out.Name(), path)
}
