package acpweb

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/coder/websocket"
)

// wsConn adapts a *websocket.Conn to this package's Conn interface, the
// only place this package ever touches the coder/websocket API directly.
type wsConn struct {
	conn *websocket.Conn
}

func (w *wsConn) ReadMessage(ctx context.Context) ([]byte, error) {
	_, data, err := w.conn.Read(ctx)
	return data, err
}

func (w *wsConn) WriteMessage(ctx context.Context, data []byte) error {
	return w.conn.Write(ctx, websocket.MessageText, data)
}

// Config is what the frontend fetches once at startup from /config: the
// workspace it should pass to session/new or session/load, and a session
// id if the bridge was started with -resume. Neither field is itself
// secret, but the endpoint is gated identically to /ws (selfOrigin/token)
// since it reveals a real local filesystem path.
type Config struct {
	Cwd    string `json:"cwd"`
	Resume string `json:"resume,omitempty"`
}

// Server serves the bridge's static frontend assets and upgrades exactly
// one browser connection at a time into a Relay, gated by the Origin
// allowlist and per-invocation token on every /ws and /config request.
// It never parses a session/update or any other ACP message that flows
// through the WebSocket it upgrades.
type Server struct {
	relay      *Relay
	assets     fs.FS
	config     Config
	token      string
	selfOrigin string
}

// NewServer constructs a Server. SetSelfOrigin must be called once the
// bridge's real listening address is known (main.go, after net.Listen)
// and before Handler serves any request.
func NewServer(relay *Relay, assets fs.FS, config Config, token string) *Server {
	return &Server{relay: relay, assets: assets, config: config, token: token}
}

// SetSelfOrigin fixes the origin CheckOrigin compares an incoming
// request's Origin header against, e.g. "http://127.0.0.1:54321".
func (s *Server) SetSelfOrigin(origin string) {
	s.selfOrigin = origin
}

// Handler returns the bridge's complete HTTP handler: static assets at
// "/", the gated config endpoint at "/config", and the gated WebSocket
// upgrade at "/ws". The static shell at "/" is intentionally left
// ungated — it carries no session data or filesystem path, only the
// application's own JS/CSS/HTML, so a bare GET on the port leaks at most
// "an acp-web-bridge is running here," not a workspace path or a live
// session.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(s.assets)))
	mux.HandleFunc("/config", s.handleConfig)
	mux.HandleFunc("/ws", s.handleWebSocket)
	return mux
}

func (s *Server) allowed(r *http.Request) bool {
	return UpgradeAllowed(s.selfOrigin, r.Header.Get("Origin"), s.token, r.URL.Query().Get("token"))
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if !s.allowed(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.config)
}

// handleWebSocket performs the upgrade-gate check before ever calling
// websocket.Accept, then wires the accepted connection into the Relay via
// SetConn and returns immediately: Accept hijacks the underlying net.Conn,
// so the socket lives independently of this handler goroutine's lifetime,
// and Relay's own background pumps (already running since Task 1) are
// what actually read and write it from here on.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !s.allowed(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// This handler already ran its own Origin check above with this
		// project's exact, independently-tested semantics (security.go);
		// the library's own pattern-based Origin check would be redundant
		// and less precise, so it is disabled rather than duplicated.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(MaxRelayFrameBytes)

	previous := s.relay.SetConn(&wsConn{conn: conn})
	if pc, ok := previous.(*wsConn); ok {
		_ = pc.conn.Close(websocket.StatusNormalClosure, "superseded by a new connection")
	}
}
