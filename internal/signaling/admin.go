package signaling

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	// adminSessionTTL 是浏览器会话的有效期：够一次运维，不至于长期有效。
	adminSessionTTL = 12 * time.Hour
	// adminCookieName 是会话 cookie 的名字。
	adminCookieName = "relaymic_admin"
	// adminLoginMaxPerMinute 是每个来源每分钟允许的登录失败次数。
	adminLoginMaxPerMinute = 8
	// adminBodyBytes 是管理面请求体上限：它只是几个字段。
	adminBodyBytes = 4 << 10
)

// adminSurface 是 Hub 的管理面：动态接收端清单，以及浏览器会话。
//
// 它是公网上的一个面，所以默认不开：没配 admin 凭据就整片 404。开了之后，
// 浏览器走会话 cookie（管理凭据不进 JS），脚本走 Bearer。
type adminSurface struct {
	digest TokenDigest
	store  *ReceiverStore
	page   []byte
	now    func() time.Time

	mu       sync.Mutex
	sessions map[string]time.Time
	attempts map[string][]time.Time
}

func newAdminSurface(digest TokenDigest, store *ReceiverStore, page []byte, now func() time.Time) *adminSurface {
	if now == nil {
		now = time.Now
	}
	return &adminSurface{
		digest:   digest,
		store:    store,
		page:     page,
		now:      now,
		sessions: make(map[string]time.Time),
		attempts: make(map[string][]time.Time),
	}
}

// authorized 判断这个请求能不能进管理面：脚本用 Bearer，浏览器用会话 cookie。
func (a *adminSurface) authorized(r *http.Request) bool {
	if a == nil {
		return false
	}
	if TokenMatches(a.digest, bearerToken(r)) {
		return true
	}
	cookie, err := r.Cookie(adminCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	expiresAt, ok := a.sessions[cookie.Value]
	if !ok {
		return false
	}
	if !a.now().Before(expiresAt) {
		delete(a.sessions, cookie.Value)
		return false
	}
	return true
}

func (a *adminSurface) newSession() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(raw)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions[id] = a.now().Add(adminSessionTTL)
	return id, nil
}

func (a *adminSurface) dropSession(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, id)
}

// allowAttempt 是登录口的限流：它在公网上，得挡住慢慢试。
func (a *adminSurface) allowAttempt(client string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	kept := a.attempts[client][:0]
	for _, ts := range a.attempts[client] {
		if now.Sub(ts) < time.Minute {
			kept = append(kept, ts)
		}
	}
	a.attempts[client] = kept
	return len(kept) < adminLoginMaxPerMinute
}

func (a *adminSurface) recordFailure(client string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.attempts[client] = append(a.attempts[client], a.now())
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path != "/admin" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	_, _ = w.Write(s.admin.page)
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	a := s.admin
	if a == nil {
		http.NotFound(w, r)
		return
	}
	client := clientKey(r)
	if !a.allowAttempt(client) {
		http.Error(w, "试得太多，过一分钟再来", http.StatusTooManyRequests)
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, adminBodyBytes)).Decode(&body); err != nil {
		http.Error(w, "请求体不是 JSON", http.StatusBadRequest)
		return
	}
	if !TokenMatches(a.digest, strings.TrimSpace(body.Token)) {
		a.recordFailure(client)
		http.Error(w, "管理口令不对", http.StatusUnauthorized)
		return
	}
	session, err := a.newSession()
	if err != nil {
		http.Error(w, "建会话失败", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    session,
		Path:     "/admin",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(adminSessionTTL.Seconds()),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		http.NotFound(w, r)
		return
	}
	if cookie, err := r.Cookie(adminCookieName); err == nil {
		s.admin.dropSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    "",
		Path:     "/admin",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

// adminReceiver 是管理面里的一台接收端。
//
// 不带配对码：那是给操作者当次使用的凭据，接收端自己的控制台上有；管理页是
// 长期开着的一页，没必要把它留在上面。
type adminReceiver struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
	Online    bool      `json:"online"`
	InCall    bool      `json:"inCall"`
	Dynamic   bool      `json:"dynamic"`
}

func (s *Server) adminReceivers() []adminReceiver {
	dynamic := make(map[string]ReceiverRecord)
	if s.admin != nil && s.admin.store != nil {
		for _, rec := range s.admin.store.List() {
			dynamic[rec.ID] = rec
		}
	}

	s.mu.Lock()
	online := make(map[string]bool, len(s.receivers))
	for id := range s.receivers {
		online[id] = true
	}
	inCall := make(map[string]bool, len(s.sessions))
	for _, sess := range s.sessions {
		if sess.receiver != nil {
			inCall[sess.receiver.id] = true
		}
	}
	s.mu.Unlock()

	out := make([]adminReceiver, 0, len(s.registry.Receivers()))
	for _, rec := range s.registry.Receivers() {
		entry := adminReceiver{
			ID:     rec.ID(),
			Name:   rec.Name(),
			Online: online[rec.ID()],
			InCall: inCall[rec.ID()],
		}
		if record, ok := dynamic[rec.ID()]; ok {
			entry.Dynamic = true
			entry.CreatedAt = record.CreatedAt
		}
		out = append(out, entry)
	}
	return out
}

func (s *Server) handleAdminReceivers(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		http.NotFound(w, r)
		return
	}
	if !s.admin.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]any{"receivers": s.adminReceivers()})
	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, adminBodyBytes)).Decode(&body); err != nil {
			http.Error(w, "请求体不是 JSON", http.StatusBadRequest)
			return
		}
		record, token, err := s.admin.store.Add(body.Name, s.admin.now())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := s.registry.Add(record); err != nil {
			_ = s.admin.store.Remove(record.ID)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.logger.Printf("管理面新增接收端: %s", record.Name)
		w.WriteHeader(http.StatusCreated)
		// 明文 token 只在这里出现一次，之后 Hub 自己也只有摘要。
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":        record.ID,
			"name":      record.Name,
			"createdAt": record.CreatedAt,
			"token":     token,
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAdminReceiver(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		http.NotFound(w, r)
		return
	}
	if !s.admin.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/api/receivers/")
	if id == "" {
		http.Error(w, "缺少 id", http.StatusBadRequest)
		return
	}

	// 清单里没有（配置里的静态接收端）就不能从管理面删：那份配置在服务器上，
	// 删了下次重启又会回来。
	if err := s.admin.store.Remove(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if !s.registry.Remove(id) {
		http.Error(w, "接收端不在册", http.StatusNotFound)
		return
	}
	s.closeReceiver(id)
	s.logger.Printf("管理面注销接收端: %s", id)
	w.WriteHeader(http.StatusNoContent)
}

// closeReceiver 掐断某台接收端当前的连接：注销要当场生效。
func (s *Server) closeReceiver(id string) {
	s.mu.Lock()
	rc := s.receivers[id]
	s.mu.Unlock()
	if rc != nil {
		_ = rc.conn.Close(websocket.StatusGoingAway, "receiver revoked")
	}
}
