package task

import (
	"testing"
	"time"

	"github.com/wabisabi926/faststrm/internal/service/sse"
)

func TestFinishCancelledTaskKeepsCancelledState(t *testing.T) {
	rt := GetRuntime()
	id := "cancel-final-state-test"
	task := &Task{ID: id, Account: "cancel-account"}

	_, cancel := rt.Register(task)
	defer func() {
		cancel()
		rt.Unregister(id)
	}()

	if got := rt.Cancel(id); !got {
		t.Fatal("Cancel should find the registered task")
	}

	res := finishCancelledTask(rt, sse.GetServer(), id, time.Now())
	if res.Success || res.Reason != "cancelled" {
		t.Fatalf("unexpected result: %+v", res)
	}
	state := rt.GetState(id)
	if state == nil {
		t.Fatal("task state missing after cancel")
	}
	if state.Status != StatusCancelled {
		t.Fatalf("status = %q, want %q", state.Status, StatusCancelled)
	}
	if state.Stage != string(StatusCancelled) {
		t.Fatalf("stage = %q, want %q", state.Stage, StatusCancelled)
	}
	if state.Error != "" {
		t.Fatalf("cancelled task should not keep an error: %q", state.Error)
	}
}

func TestRuntimeWaitWaitsForExecutorExit(t *testing.T) {
	rt := GetRuntime()
	id := "runtime-wait-test"
	task := &Task{ID: id, Account: "wait-account"}
	rt.Register(task)
	done := make(chan bool, 1)
	go func() {
		done <- rt.Wait(id, time.Second)
	}()
	time.Sleep(30 * time.Millisecond)
	rt.Cancel(id)
	// Cancel 只改状态，不代表执行器已退出；Wait 必须等 Unregister。
	select {
	case <-done:
		t.Fatal("Wait returned before executor unregistered")
	case <-time.After(50 * time.Millisecond):
	}
	rt.Unregister(id)
	if !<-done {
		t.Fatal("Wait should return true after unregister")
	}
}
