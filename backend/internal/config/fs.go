package config

import (
	"os"
	"time"
)

// 文件操作小包装，便于测试替换/统一错误处理。

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }
func writeFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}
func renameFile(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }
func removeFile(path string) error             { return os.Remove(path) }
func isNotExist(err error) bool                { return os.IsNotExist(err) }

func statFile(path string) (time.Time, error) {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return st.ModTime(), nil
}
