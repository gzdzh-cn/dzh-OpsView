package executor

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func compressRotatedLogs(ctx context.Context, dir string) error {
	files, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(path+".gz", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			in.Close()
			return err
		}
		gz := gzip.NewWriter(out)
		_, err = io.Copy(gz, in)
		err = errors.Join(err, gz.Close(), out.Close(), in.Close())
		if err != nil {
			_ = os.Remove(path + ".gz")
			return err
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
