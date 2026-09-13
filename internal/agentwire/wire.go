// Package agentwire defines the bounded, single-task Windows Agent protocol.
package agentwire

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const MaxCommand = 64 * 1024
const MaxOutput = 8 * 1024 * 1024
const TaskTimeout = 5 * time.Minute

type Message struct {
	Type    string `json:"type"`
	ID      string `json:"id,omitempty"`
	Command string `json:"command,omitempty"`
	Data    []byte `json:"data,omitempty"`
	Code    int    `json:"code,omitempty"`
}

type Conn struct {
	*websocket.Conn
	mu sync.Mutex
}

func Wrap(c *websocket.Conn) *Conn {
	c.SetReadLimit(512 * 1024)
	c.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.SetPongHandler(func(string) error { return c.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	return &Conn{Conn: c}
}

func (c *Conn) Send(m Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.WriteJSON(m)
}

// Heartbeat must run alongside a reader, which processes incoming control frames.
func (c *Conn) Heartbeat(done <-chan struct{}) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			if c.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)) != nil {
				c.Close()
				return
			}
		}
	}
}
