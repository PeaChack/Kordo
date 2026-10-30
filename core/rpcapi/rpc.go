// Package rpcapi implements line-delimited JSON-RPC 2.0 used between the
// service, the GUI and the CLI, plus the shared request/response types.
package rpcapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const ProtocolVersion = 1

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"` // notifications
	Params  json.RawMessage `json:"params,omitempty"` // notifications
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

const maxLine = 16 << 20

// ---------------------------------------------------------------- server

type Handler func(ctx context.Context, c *Conn, method string, params json.RawMessage) (any, error)

type Server struct {
	handler Handler
	log     *slog.Logger
	mu      sync.Mutex
	conns   map[*Conn]struct{}
}

type Conn struct {
	net.Conn
	wmu  sync.Mutex
	Info any // platform data (e.g. client session id)
}

func (c *Conn) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = c.Write(append(b, '\n'))
	return err
}

func NewServer(h Handler, log *slog.Logger) *Server {
	return &Server{handler: h, log: log, conns: map[*Conn]struct{}{}}
}

// Serve accepts connections until the listener is closed. accept may attach
// platform info or reject a connection by returning an error.
func (s *Server) Serve(l net.Listener, accept func(net.Conn) (any, error)) error {
	for {
		nc, err := l.Accept()
		if err != nil {
			return err
		}
		var info any
		if accept != nil {
			if info, err = accept(nc); err != nil {
				s.log.Warn("rpc: rejected client", "err", err)
				nc.Close()
				continue
			}
		}
		c := &Conn{Conn: nc, Info: info}
		s.mu.Lock()
		s.conns[c] = struct{}{}
		s.mu.Unlock()
		go s.serveConn(c)
	}
}

func (s *Server) serveConn(c *Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		c.Close()
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := bufio.NewReaderSize(c, 64<<10)
	for {
		line, err := readLine(r)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				s.log.Debug("rpc: read", "err", err)
			}
			return
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = c.send(Response{JSONRPC: "2.0", Error: &Error{Code: -32700, Message: "parse error"}})
			continue
		}
		go func(req Request) {
			defer func() {
				if p := recover(); p != nil {
					s.log.Error("rpc: handler panic", "method", req.Method, "panic", p)
					if req.ID != nil {
						_ = c.send(Response{JSONRPC: "2.0", ID: req.ID, Error: &Error{Code: -32603, Message: fmt.Sprint("internal error: ", p)}})
					}
				}
			}()
			res, err := s.handler(ctx, c, req.Method, req.Params)
			if req.ID == nil {
				return
			}
			resp := Response{JSONRPC: "2.0", ID: req.ID}
			if err != nil {
				var re *Error
				if errors.As(err, &re) {
					resp.Error = re
				} else {
					resp.Error = &Error{Code: -32000, Message: err.Error()}
				}
			} else {
				b, mErr := json.Marshal(res)
				if mErr != nil {
					resp.Error = &Error{Code: -32603, Message: mErr.Error()}
				} else {
					resp.Result = b
				}
			}
			_ = c.send(resp)
		}(req)
	}
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		buf = append(buf, chunk...)
		if len(buf) > maxLine {
			return nil, errors.New("rpc: line too long")
		}
		if !isPrefix {
			return buf, nil
		}
	}
}

// Notify broadcasts a notification to every connected client.
func (s *Server) Notify(method string, params any) {
	b, err := json.Marshal(params)
	if err != nil {
		return
	}
	s.mu.Lock()
	conns := make([]*Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		go func(c *Conn) { _ = c.send(Response{JSONRPC: "2.0", Method: method, Params: b}) }(c)
	}
}

// ---------------------------------------------------------------- client

type Client struct {
	conn    net.Conn
	wmu     sync.Mutex
	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan Response
	onNote  func(method string, params json.RawMessage)
	done    chan struct{}
	err     error
}

func NewClient(conn net.Conn, onNotification func(method string, params json.RawMessage)) *Client {
	c := &Client{conn: conn, pending: map[int64]chan Response{}, onNote: onNotification, done: make(chan struct{})}
	go c.readLoop()
	return c
}

func (c *Client) Done() <-chan struct{} { return c.done }
func (c *Client) Close() error          { return c.conn.Close() }

func (c *Client) readLoop() {
	r := bufio.NewReaderSize(c.conn, 64<<10)
	defer close(c.done)
	for {
		line, err := readLine(r)
		if err != nil {
			c.mu.Lock()
			c.err = err
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		var resp Response
		if json.Unmarshal(line, &resp) != nil {
			continue
		}
		if resp.ID == nil {
			if resp.Method != "" && c.onNote != nil {
				c.onNote(resp.Method, resp.Params)
			}
			continue
		}
		c.mu.Lock()
		ch := c.pending[*resp.ID]
		delete(c.pending, *resp.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
}

// CallRaw performs a call and returns the raw JSON result.
func (c *Client) CallRaw(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	var p json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		p = b
	}
	ch := make(chan Response, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("rpc: connection closed: %w", c.err)
	}
	c.pending[id] = ch
	c.mu.Unlock()
	b, _ := json.Marshal(Request{JSONRPC: "2.0", ID: &id, Method: method, Params: p})
	c.wmu.Lock()
	_, err := c.conn.Write(append(b, '\n'))
	c.wmu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, errors.New("rpc: connection closed")
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	raw, err := c.CallRaw(ctx, method, params)
	if err != nil {
		return err
	}
	if result == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, result)
}
