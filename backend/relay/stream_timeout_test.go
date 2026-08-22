package relay

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 回归：流式请求必须用无 Timeout 的 streamClient。构造一个 Timeout=1s 的 adapter，
// 上游延迟 1.5s 才出首字节、之后持续推送——若流式误用带超时的 client 会被切断，
// 用 streamClient 则正常完整收到。
func TestOpenAIStreamNotCutByClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl, _ := w.(http.Flusher)
		// 首字节延迟 1.5s（超过 client 的 1s Timeout）。
		time.Sleep(1500 * time.Millisecond)
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "data: {\"chunk\":%d}\n\n", i)
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(100 * time.Millisecond)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
	defer srv.Close()

	// 关键：Timeout=1s。非流式会被掐断，流式（streamClient 无超时）不会。
	a := NewOpenAIAdapter(1 * time.Second)
	resp, err := a.SendRequestStream(context.Background(), srv.URL, "k", []byte(`{"stream":true}`))
	if err != nil {
		t.Fatalf("stream request should not be cut by client timeout, got: %v", err)
	}
	defer resp.Body.Close()

	// 完整读完流（耗时 >1.8s，远超 1s Timeout）。
	scanner := bufio.NewScanner(resp.Body)
	var lines int
	gotDone := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "data:") {
			lines++
		}
		if strings.Contains(line, "[DONE]") {
			gotDone = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("stream read interrupted (timeout cut the connection?): %v", err)
	}
	if !gotDone || lines < 4 {
		t.Fatalf("expected full stream incl [DONE], got %d data lines, done=%v", lines, gotDone)
	}
}

// 对照：非流式请求仍受 Timeout 约束。同样 1s Timeout + 1.5s 延迟 → 应超时报错。
func TestOpenAINonStreamStillRespectsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[]}`)
	}))
	defer srv.Close()

	a := NewOpenAIAdapter(1 * time.Second)
	_, _, _, err := a.SendRequestRawWithBody(context.Background(), srv.URL, "k", []byte(`{}`))
	if err == nil {
		t.Fatalf("non-stream request should be cut by 1s client timeout, but succeeded")
	}
}
