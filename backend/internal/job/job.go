// Package job 提供内存中的异步任务管理（内核下载/安装等）。
package job

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// 任务状态。
const (
	StateRunning = "running"
	StateDone    = "done"
	StateError   = "error"
)

// JobView 是任务对外的只读快照（时间统一为 RFC3339 字符串）。
type JobView struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	State      string `json:"state"`
	Progress   int    `json:"progress"`
	Message    string `json:"message"`
	Error      string `json:"error,omitempty"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt,omitempty"`
	Result     any    `json:"result,omitempty"`
}

// Job 是一个异步任务（内部使用，含互斥锁，不直接序列化）。
type Job struct {
	mu         sync.RWMutex
	id         string
	kind       string
	state      string
	progress   int
	message    string
	errText    string
	startedAt  time.Time
	finishedAt time.Time
	result     any
}

// Snapshot 返回任务当前状态的副本。
func (j *Job) Snapshot() JobView {
	j.mu.RLock()
	defer j.mu.RUnlock()
	v := JobView{
		ID:        j.id,
		Type:      j.kind,
		State:     j.state,
		Progress:  j.progress,
		Message:   j.message,
		Error:     j.errText,
		StartedAt: j.startedAt.Format(time.RFC3339),
		Result:    j.result,
	}
	if !j.finishedAt.IsZero() {
		v.FinishedAt = j.finishedAt.Format(time.RFC3339)
	}
	return v
}

// Update 更新进度与消息。
func (j *Job) Update(progress int, message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	j.progress = progress
	if message != "" {
		j.message = message
	}
}

// SetMessage 只更新消息。
func (j *Job) SetMessage(message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.message = message
}

// Done 标记任务成功完成。
func (j *Job) Done(result any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.state = StateDone
	j.progress = 100
	j.result = result
	j.finishedAt = time.Now()
	if j.message == "" {
		j.message = "已完成"
	}
}

// Fail 标记任务失败。
func (j *Job) Fail(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.state = StateError
	j.errText = err.Error()
	j.finishedAt = time.Now()
}

// Manager 管理所有任务。
type Manager struct {
	mu    sync.RWMutex
	jobs  map[string]*Job
	order []string
}

// NewManager 创建任务管理器。
func NewManager() *Manager {
	return &Manager{jobs: map[string]*Job{}}
}

// Start 创建一个运行中的任务。
func (m *Manager) Start(kind string) *Job {
	j := &Job{
		id:        newID(),
		kind:      kind,
		state:     StateRunning,
		progress:  0,
		message:   "任务已开始",
		startedAt: time.Now(),
	}
	m.mu.Lock()
	m.jobs[j.id] = j
	m.order = append(m.order, j.id)
	if len(m.order) > 100 {
		// 保留最近 100 个任务
		for _, oldID := range m.order[:len(m.order)-100] {
			delete(m.jobs, oldID)
		}
		m.order = append([]string(nil), m.order[len(m.order)-100:]...)
	}
	m.mu.Unlock()
	return j
}

// Get 查询任务快照。
func (m *Manager) Get(id string) (JobView, bool) {
	m.mu.RLock()
	j, ok := m.jobs[id]
	m.mu.RUnlock()
	if !ok {
		return JobView{}, false
	}
	return j.Snapshot(), true
}

// List 返回最近的任务快照（新到旧）。
func (m *Manager) List() []JobView {
	m.mu.RLock()
	ids := append([]string(nil), m.order...)
	m.mu.RUnlock()
	out := make([]JobView, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		if j, ok := m.Get(ids[i]); ok {
			out = append(out, j)
		}
	}
	return out
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000")))
	}
	return hex.EncodeToString(b)
}
