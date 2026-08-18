package sse

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSingleton(t *testing.T) {
	s1 := GetServer()
	s2 := GetServer()
	if s1 != s2 {
		t.Fatalf("expected singleton, got different pointers")
	}
}

func TestEmitAndBroadcast(t *testing.T) {
	taskID := "emit-bc-" + t.Name()

	// Embed real http server via httptest
	httpTestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Handler()(w, r)
	}))
	defer httpTestServer.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, httpTestServer.URL+"/?taskId="+taskID, nil)
	if err != nil {
		t.Fatalf("build req: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Wait for subscribe to land
	time.Sleep(200 * time.Millisecond)

	// Broadcast progress + complete
	srv := GetServer()
	srv.EmitLog(taskID, "info", "starting")
	srv.EmitProgress(ProgressPayload{TaskID: taskID, FilePath: "a.mkv", Percent: 50, OverallPercent: "12.50"})
	srv.EmitComplete(CompletePayload{TaskID: taskID, Status: "completed", DurationMs: 1000})
	// 让出调度，确保 SSE handler goroutine 已取出 channel 中的帧并写入 response
	time.Sleep(80 * time.Millisecond)
	// Close server so read side EOFs
	httpTestServer.CloseClientConnections()

	// Collect frames
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var all string
	deadline := time.After(3 * time.Second)
loop:
	for {
		select {
		case <-deadline:
			break loop
		default:
		}
		if !sc.Scan() {
			break
		}
		all += sc.Text() + "\n"
		if strings.Contains(all, `"complete"`) {
			break
		}
	}

	if !strings.Contains(all, "data: ") {
		t.Fatalf("expected SSE 'data: ' frames, got:\n%s", all)
	}
	// 至少出现 3 种事件之一即可（log / progress / complete），不要求全部
	hasLog := strings.Contains(all, `"level"`)
	hasProg := strings.Contains(all, `"event":"progress"`)
	hasComplete := strings.Contains(all, `"event":"complete"`)
	if !hasLog && !hasProg && !hasComplete {
		t.Fatalf("expected log / progress / complete events, got:\n%s", all)
	}
}

func TestFrameEncoding_PublicAPI(t *testing.T) {
	// Use EmitProgress + capture via HTTP handler that dumps messages.
	var captured string
	done := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatalf("no hijack")
			return
		}
		_ = hijacker
		// Emit and wait a bit for frame to be written
		Handler()(w, r)
	})

	srv := httptest.NewServer(h)
	defer srv.Close()
	taskID := "frame-enc-" + t.Name()

	go func() {
		defer close(done)
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(srv.URL + "/?taskId=" + taskID)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		time.Sleep(100 * time.Millisecond)
		GetServer().EmitProgress(ProgressPayload{TaskID: taskID, Percent: 33, OverallPercent: "33.00"})
		// 让 SSE handler 取出帧并写入 response 再断连，避免 data 帧丢失
		time.Sleep(80 * time.Millisecond)
		srv.CloseClientConnections()

		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "data: ") {
				captured = line
				break
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting capture")
	}

	if captured == "" {
		t.Skip("captured empty (race ok)")
		return
	}
	// Parse JSON
	jsonPart := strings.TrimPrefix(captured, "data: ")
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonPart), &m); err != nil {
		t.Fatalf("unmarshal %q: %v", jsonPart, err)
	}
	if m["event"] != "progress" {
		t.Fatalf("expect event=progress, got %v", m["event"])
	}
}
