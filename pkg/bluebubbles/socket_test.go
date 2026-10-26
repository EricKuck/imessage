package bluebubbles

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rs/zerolog"
)

func TestRunSocketOnce(t *testing.T) {
	gotEvents := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("password") != "hunter2" || r.URL.Query().Get("EIO") != "4" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept failed: %v", err)
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()
		expect := func(want string) {
			_, data, err := conn.Read(ctx)
			if err != nil || string(data) != want {
				t.Errorf("expected %q, got %q (%v)", want, data, err)
			}
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(`0{"sid":"abc","pingInterval":25000,"pingTimeout":20000}`))
		expect("40")
		_ = conn.Write(ctx, websocket.MessageText, []byte(`40{"sid":"def"}`))
		_ = conn.Write(ctx, websocket.MessageText, []byte(`2`))
		expect("3")
		_ = conn.Write(ctx, websocket.MessageText, []byte(`42["new-message",{"guid":"msg1"}]`))
		_ = conn.Write(ctx, websocket.MessageText, []byte(`42["typing-indicator"]`))
		_ = conn.Write(ctx, websocket.MessageText, []byte(`41`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "hunter2", server.Client(), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	connected := false
	handler := func(name string, data json.RawMessage) {
		gotEvents <- name + ":" + string(data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wasConnected, err := client.runSocketOnce(ctx, handler, SocketCallbacks{OnConnect: func() { connected = true }})
	if !wasConnected || !connected {
		t.Errorf("expected connected, got %v/%v", wasConnected, connected)
	}
	if err == nil || !strings.Contains(err.Error(), "disconnected") {
		t.Errorf("expected disconnect error, got %v", err)
	}
	if evt := <-gotEvents; evt != `new-message:{"guid":"msg1"}` {
		t.Errorf("unexpected event %q", evt)
	}
	if evt := <-gotEvents; evt != "typing-indicator:" {
		t.Errorf("unexpected event %q", evt)
	}
}

func TestAPIErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("password") != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":401,"message":"You are not authorized to access this resource","error":{"type":"Authentication Error","message":"Unauthorized"}}`))
			return
		}
		switch r.URL.Path {
		case "/api/v1/server/info":
			_, _ = w.Write([]byte(`{"status":200,"message":"ok","data":{"private_api":true,"detected_icloud":"me@example.com"}}`))
		case "/api/v1/chat/iMessage;-;+15551234567":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":404,"message":"The requested resource was not found","error":{"type":"Database Error","message":"Chat does not exist!"}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, "pw", server.Client(), zerolog.Nop())
	info, err := client.GetServerInfo(context.Background())
	if err != nil || !info.PrivateAPI || info.DetectedICloud != "me@example.com" {
		t.Fatalf("unexpected server info %+v (%v)", info, err)
	}
	_, err = client.GetChat(context.Background(), "iMessage;-;+15551234567")
	apiErr, ok := err.(*APIError)
	if !ok || !apiErr.IsNotFound() || apiErr.Detail != "Chat does not exist!" {
		t.Fatalf("expected not found error, got %v", err)
	}
	client.Password = "wrong"
	_, err = client.GetServerInfo(context.Background())
	if apiErr, ok = err.(*APIError); !ok || !apiErr.IsUnauthorized() {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
}
