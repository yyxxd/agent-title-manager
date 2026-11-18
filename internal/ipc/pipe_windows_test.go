package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

func TestPipeAllowlist(t *testing.T) {
	scope := fmt.Sprintf(".test-%d", os.Getpid())
	listener, err := Listen(scope)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); listener.Close() })
	var calls atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, listener, func(_ context.Context, method string, p json.RawMessage) (any, error) {
			calls.Add(1)
			if method != "title.claim" || !strings.Contains(string(p), "中文") {
				t.Error("request changed")
			}
			return map[string]string{"title": "标题"}, nil
		})
	}()
	request := Request{V: 1, Method: "title.claim", Params: json.RawMessage(`{"currentTitle":"中文"}`)}
	response, err := Call(ctx, scope, request)
	if err != nil || !response.OK || string(response.Result) != `{"title":"标题"}` {
		t.Fatalf("%+v, %v", response, err)
	}
	testBlockedPipe(t, ctx, scope)
	if calls.Load() != 1 {
		t.Fatal("untrusted method reached handler")
	}
	listener.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func testBlockedPipe(t *testing.T, parent context.Context, scope string) {
	t.Helper()
	name, _, err := identity(scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"settings.get", "settings.save", "providers.test", "plugins.install", "sync.adopt"} {
		ctx, cancel := context.WithTimeout(parent, time.Second)
		conn, err := winio.DialPipeContext(ctx, name)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(time.Second))
		fmt.Fprintf(conn, "{\"v\":1,\"method\":%q,\"params\":{}}\n", method)
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		conn.Close()
		if err != nil || !strings.Contains(string(line), `"ok":false`) {
			t.Fatalf("%s: %q, %v", method, line, err)
		}
	}
}
