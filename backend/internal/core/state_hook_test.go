package core

import (
	"sync/atomic"
	"testing"
)

// TestStateHookNilSafe 验证未注册/空回调时触发 hook 不会 panic，Stop 也正常返回。
func TestStateHookNilSafe(t *testing.T) {
	m, _ := newTestManager(t)
	m.notifyStateHook()
	if err := m.Stop(); err != nil {
		t.Fatalf("未注册状态回调时 Stop 失败: %v", err)
	}
}

// TestStateHookCalledOnStop 验证 Stop 结束时会触发状态回调（模块描述刷新依赖它）。
func TestStateHookCalledOnStop(t *testing.T) {
	m, _ := newTestManager(t)
	var calls int32
	m.SetStateHook(func() { atomic.AddInt32(&calls, 1) })
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	if atomic.LoadInt32(&calls) == 0 {
		t.Fatal("Stop 结束应触发状态回调")
	}
}

// TestStateHookPanicRecovered 验证回调 panic 会被 recover，不影响内核生命周期流程。
func TestStateHookPanicRecovered(t *testing.T) {
	m, _ := newTestManager(t)
	m.SetStateHook(func() { panic("boom") })
	if err := m.Stop(); err != nil {
		t.Fatalf("回调 panic 不应影响 Stop: %v", err)
	}
}
