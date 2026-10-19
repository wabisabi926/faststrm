package notify

import (
	"errors"
	"testing"
	"time"
)

// TestSendGuard_BackoffAndRecover 连续失败进入退避，退避到期后恢复，成功复位
func TestSendGuard_BackoffAndRecover(t *testing.T) {
	oldLog, oldMax, oldBackoff := sendGuardLogInterval, sendGuardMaxFailures, sendGuardBackoffPeriod
	sendGuardLogInterval = time.Millisecond
	sendGuardMaxFailures = 3
	sendGuardBackoffPeriod = 20 * time.Millisecond
	defer func() {
		sendGuardLogInterval, sendGuardMaxFailures, sendGuardBackoffPeriod = oldLog, oldMax, oldBackoff
	}()

	var g sendGuard
	if !g.allow() {
		t.Fatal("初始应允许发送")
	}
	for i := 0; i < 3; i++ {
		g.onFailure(errors.New("i/o timeout"))
	}
	if g.allow() {
		t.Fatal("连续失败达到阈值后应进入退避")
	}
	time.Sleep(30 * time.Millisecond)
	if !g.allow() {
		t.Fatal("退避到期后应恢复发送")
	}
	// 成功复位：再次失败不会立刻进入退避
	g.onSuccess()
	for i := 0; i < 2; i++ {
		g.onFailure(errors.New("x"))
	}
	if !g.allow() {
		t.Fatal("成功复位后未达阈值不应退避")
	}
}

// TestSendGuard_SuccessAfterBackoff 退避后发送成功应清除退避状态
func TestSendGuard_SuccessAfterBackoff(t *testing.T) {
	oldMax, oldBackoff := sendGuardMaxFailures, sendGuardBackoffPeriod
	sendGuardMaxFailures = 1
	sendGuardBackoffPeriod = time.Hour
	defer func() { sendGuardMaxFailures, sendGuardBackoffPeriod = oldMax, oldBackoff }()

	var g sendGuard
	g.onFailure(errors.New("timeout"))
	if g.allow() {
		t.Fatal("应处于退避中")
	}
	g.onSuccess()
	if !g.allow() {
		t.Fatal("onSuccess 后应立即恢复")
	}
}
