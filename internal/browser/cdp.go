package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	dialTimeout = 5 * time.Second
	readLimit   = 64 << 20 // un message CDP peut peser plusieurs Mo
)

// message : ce qu'envoie le navigateur, réponse (ID) ou événement (Method). Le
// décodeur JSON ignore la casse : pas besoin d'étiquettes pour « sessionId ».
type message struct {
	ID             int64
	SessionID      string
	Method         string
	Params, Result json.RawMessage
	Error          *struct{ Message string }
}

// conn : la connexion WebSocket au navigateur, une seule pour tous les onglets.
// Chaque onglet est une session « à plat » (sessionId dans chaque message).
type conn struct {
	ws      *websocket.Conn
	onEvent func(*conn, message) // appelé par la boucle de lecture : ne doit pas bloquer
	done    chan struct{}        // fermé quand la connexion est perdue
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan message
}

func dial(wsURL string, onEvent func(*conn, message)) (*conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(readLimit)
	c := &conn{ws: ws, onEvent: onEvent, done: make(chan struct{}), pending: map[int64]chan message{}}
	go c.readLoop()
	return c, nil
}

func (c *conn) close() { _ = c.ws.CloseNow() }

func (c *conn) readLoop() {
	defer close(c.done)
	for {
		_, data, err := c.ws.Read(context.Background())
		if err != nil {
			c.close()
			return
		}
		var m message
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.ID == 0 {
			c.onEvent(c, m)
			continue
		}
		c.mu.Lock()
		ch := c.pending[m.ID]
		delete(c.pending, m.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
}

// call envoie une commande et attend sa réponse (session vide = le navigateur
// lui-même). out, s'il est donné, reçoit le résultat décodé.
func (c *conn) call(session, method string, params map[string]any, out any, timeout time.Duration) error {
	return c.send(session, method, params)(out, timeout)
}

// send écrit une commande sans attendre ; la fonction rendue attend sa réponse.
// Utile quand plusieurs commandes doivent partir dans l'ordre avant qu'aucune
// ne réponde (voir equip).
func (c *conn) send(session, method string, params map[string]any) func(out any, timeout time.Duration) error {
	ch := make(chan message, 1)
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()
	req := map[string]any{"id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	if session != "" {
		req["sessionId"] = session
	}
	raw, err := json.Marshal(req)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		err = c.ws.Write(ctx, websocket.MessageText, raw)
		cancel()
	}
	return func(out any, timeout time.Duration) error {
		defer func() {
			c.mu.Lock()
			delete(c.pending, id)
			c.mu.Unlock()
		}()
		if err != nil {
			return errors.New("le navigateur ne répond plus")
		}
		select {
		case m := <-ch:
			if m.Error != nil {
				return fmt.Errorf("%s : %s", method, m.Error.Message)
			}
			if out != nil {
				return json.Unmarshal(m.Result, out)
			}
			return nil
		case <-c.done:
			return errors.New("le navigateur s'est fermé")
		case <-time.After(timeout):
			return fmt.Errorf("%s : délai dépassé", method)
		}
	}
}
