package bluebubbles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"
)

// EventHandler receives socket.io events. It must not block for long.
type EventHandler func(name string, data json.RawMessage)

// SocketCallbacks receive connection state changes from RunSocket.
type SocketCallbacks struct {
	OnConnect    func()
	OnDisconnect func(err error)
}

const (
	socketReadLimit     = 64 * 1024 * 1024
	defaultPingInterval = 25 * time.Second
	defaultPingTimeout  = 20 * time.Second
	maxReconnectBackoff = time.Minute
)

var ErrSocketRejected = errors.New("websocket connection was rejected")

func (c *Client) socketURL() string {
	u := *c.URL
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = "/socket.io/"
	u.RawQuery = url.Values{
		"EIO":       {"4"},
		"transport": {"websocket"},
		"password":  {c.Password},
	}.Encode()
	return u.String()
}

// RunSocket keeps a socket.io connection to the server open until ctx is cancelled,
// reconnecting with backoff after errors.
func (c *Client) RunSocket(ctx context.Context, handler EventHandler, callbacks SocketCallbacks) {
	backoff := time.Second
	for {
		connected, err := c.runSocketOnce(ctx, handler, callbacks)
		if ctx.Err() != nil {
			return
		}
		if callbacks.OnDisconnect != nil {
			callbacks.OnDisconnect(err)
		}
		if connected {
			backoff = time.Second
		}
		c.Log.Warn().Err(err).Dur("retry_in", backoff).Msg("BlueBubbles websocket disconnected, reconnecting")
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxReconnectBackoff)
	}
}

type engineIOHandshake struct {
	PingInterval int `json:"pingInterval"`
	PingTimeout  int `json:"pingTimeout"`
}

func (c *Client) runSocketOnce(ctx context.Context, handler EventHandler, callbacks SocketCallbacks) (connected bool, err error) {
	// The shared client has a global timeout, which would kill a long-lived socket.
	conn, _, err := websocket.Dial(ctx, c.socketURL(), &websocket.DialOptions{HTTPClient: &http.Client{Transport: c.HTTP.Transport}})
	if err != nil {
		return false, fmt.Errorf("failed to dial: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(socketReadLimit)

	readTimeout := defaultPingInterval + defaultPingTimeout
	for {
		readCtx, cancel := context.WithTimeout(ctx, readTimeout)
		_, payload, err := conn.Read(readCtx)
		cancel()
		if err != nil {
			return connected, fmt.Errorf("failed to read: %w", err)
		}
		if len(payload) == 0 {
			continue
		}
		// Engine.IO packet type is the first byte, socket.io packet type the second.
		switch payload[0] {
		case '0':
			var handshake engineIOHandshake
			if json.Unmarshal(payload[1:], &handshake) == nil && handshake.PingInterval > 0 {
				readTimeout = time.Duration(handshake.PingInterval+handshake.PingTimeout) * time.Millisecond
			}
			if err = conn.Write(ctx, websocket.MessageText, []byte("40")); err != nil {
				return connected, fmt.Errorf("failed to send connect packet: %w", err)
			}
		case '2':
			if err = conn.Write(ctx, websocket.MessageText, []byte("3")); err != nil {
				return connected, fmt.Errorf("failed to send pong: %w", err)
			}
		case '1':
			return connected, errors.New("server closed the connection")
		case '4':
			if len(payload) < 2 {
				continue
			}
			switch payload[1] {
			case '0':
				connected = true
				if callbacks.OnConnect != nil {
					callbacks.OnConnect()
				}
			case '1':
				return connected, errors.New("server disconnected the socket")
			case '4':
				return connected, fmt.Errorf("%w: %s", ErrSocketRejected, bytes.TrimSpace(payload[2:]))
			case '2':
				c.dispatchEvent(payload[2:], handler)
			}
		}
	}
}

func (c *Client) dispatchEvent(payload []byte, handler EventHandler) {
	var parts []json.RawMessage
	if err := json.Unmarshal(payload, &parts); err != nil || len(parts) == 0 {
		c.Log.Warn().Err(err).Msg("Failed to parse websocket event")
		return
	}
	var name string
	if err := json.Unmarshal(parts[0], &name); err != nil {
		c.Log.Warn().Err(err).Msg("Failed to parse websocket event name")
		return
	}
	var data json.RawMessage
	if len(parts) > 1 {
		data = parts[1]
	}
	handler(name, data)
}
