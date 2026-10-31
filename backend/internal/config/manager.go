package config

import (
	_ "embed"
	"fmt"
	"sync"
	"time"

	"mproxy/internal/sysutil"
)

//go:embed default.yaml
var defaultTemplate string

// NewDefault 用内置模板生成默认配置，并写入随机 secret。
func NewDefault(secret string) (*Doc, error) {
	doc, err := Parse([]byte(defaultTemplate))
	if err != nil {
		return nil, fmt.Errorf("内置默认配置损坏: %w", err)
	}
	doc.EnsureDefaults(secret)
	return doc, nil
}

// NewDefaultWithRandomSecret 生成默认配置，secret 使用密码学随机数。
func NewDefaultWithRandomSecret() (*Doc, error) {
	secret, err := sysutil.RandomHex(16)
	if err != nil {
		return nil, fmt.Errorf("生成随机 secret 失败: %w", err)
	}
	return NewDefault(secret)
}

// Manager 管理磁盘上的唯一 config.yaml。
type Manager struct {
	mu      sync.RWMutex
	paths   sysutil.Paths
	doc     *Doc
	raw     []byte
	modTime time.Time
	exists  bool
	loadErr error
}

// Open 打开（不存在时生成）配置文件。
func Open(paths sysutil.Paths) (*Manager, error) {
	if err := paths.EnsureDirs(); err != nil {
		return nil, err
	}
	m := &Manager{paths: paths}
	data, err := readFile(paths.ConfigPath())
	if err != nil {
		if !isNotExist(err) {
			return nil, fmt.Errorf("读取配置失败: %w", err)
		}
		doc, derr := NewDefaultWithRandomSecret()
		if derr != nil {
			return nil, derr
		}
		if _, serr := m.saveLocked(doc, false); serr != nil {
			return nil, serr
		}
		return m, nil
	}
	m.raw = data
	m.exists = true
	if st, statErr := statFile(paths.ConfigPath()); statErr == nil {
		m.modTime = st
	}
	doc, perr := Parse(data)
	if perr != nil {
		m.loadErr = perr
	} else if verr := doc.ValidateStructure(); verr != nil {
		m.doc = doc
		m.loadErr = verr
	} else {
		m.doc = doc
	}
	return m, nil
}

// Paths 返回数据路径。
func (m *Manager) Paths() sysutil.Paths { return m.paths }

// Doc 返回配置副本。
func (m *Manager) Doc() (*Doc, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.doc == nil {
		if m.loadErr != nil {
			return nil, fmt.Errorf("配置文件解析失败: %w", m.loadErr)
		}
		return nil, fmt.Errorf("配置文件尚未加载")
	}
	return m.doc.Clone(), nil
}

// Raw 返回磁盘上的原始 YAML 文本（即使解析失败也保留原文）。
func (m *Manager) Raw() ([]byte, time.Time, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]byte(nil), m.raw...), m.modTime, m.exists, nil
}

// LoadError 返回最近一次加载错误（nil 表示配置有效）。
func (m *Manager) LoadError() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loadErr
}

// Summary 返回配置摘要。
func (m *Manager) Summary() (Summary, error) {
	doc, err := m.Doc()
	if err != nil {
		return Summary{}, err
	}
	return doc.Summary(), nil
}

// Save 原子保存配置：校验通过后写临时文件，备份旧配置，再 rename。
func (m *Manager) Save(doc *Doc) ([]byte, error) {
	if err := doc.ValidateStructure(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked(doc, true)
}

func (m *Manager) saveLocked(doc *Doc, backup bool) ([]byte, error) {
	raw, err := doc.Marshal()
	if err != nil {
		return nil, err
	}
	path := m.paths.ConfigPath()
	tmp := path + ".tmp"
	if err := writeFile(tmp, raw, 0o644); err != nil {
		return nil, fmt.Errorf("写入临时配置失败: %w", err)
	}
	if backup {
		if old, rerr := readFile(path); rerr == nil && len(old) > 0 {
			if err := writeFile(m.paths.BackupPath()+".tmp", old, 0o644); err != nil {
				_ = removeFile(tmp)
				return nil, fmt.Errorf("写入备份失败: %w", err)
			}
			if err := renameFile(m.paths.BackupPath()+".tmp", m.paths.BackupPath()); err != nil {
				_ = removeFile(tmp)
				return nil, fmt.Errorf("保存备份失败: %w", err)
			}
		}
	}
	if err := renameFile(tmp, path); err != nil {
		_ = removeFile(tmp)
		return nil, fmt.Errorf("保存配置失败: %w", err)
	}
	m.doc = doc
	m.raw = raw
	m.exists = true
	m.loadErr = nil
	if st, serr := statFile(path); serr == nil {
		m.modTime = st
	} else {
		m.modTime = time.Now()
	}
	return raw, nil
}

// ReloadFromDisk 重新从磁盘加载配置（用于外部编辑后刷新）。
func (m *Manager) ReloadFromDisk() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := readFile(m.paths.ConfigPath())
	if err != nil {
		return err
	}
	m.raw = data
	m.exists = true
	if st, serr := statFile(m.paths.ConfigPath()); serr == nil {
		m.modTime = st
	}
	doc, perr := Parse(data)
	if perr != nil {
		m.doc = nil
		m.loadErr = perr
		return perr
	}
	m.doc = doc
	if verr := doc.ValidateStructure(); verr != nil {
		m.loadErr = verr
	} else {
		m.loadErr = nil
	}
	return nil
}

// Backup 返回备份文件内容。
func (m *Manager) Backup() ([]byte, error) {
	return readFile(m.paths.BackupPath())
}

// HasBackup 判断是否存在备份。
func (m *Manager) HasBackup() bool {
	_, err := statFile(m.paths.BackupPath())
	return err == nil
}

// BackupModTime 返回备份修改时间。
func (m *Manager) BackupModTime() time.Time {
	st, err := statFile(m.paths.BackupPath())
	if err != nil {
		return time.Time{}
	}
	return st
}
