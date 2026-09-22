package signaling

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newAdminHub 起一个开了管理面的 Hub：admin 凭据 + 动态清单。
func newAdminHub(t *testing.T) (*testHub, *ReceiverStore, string) {
	t.Helper()
	adminToken := mustNewToken(t)
	store, err := LoadReceiverStore(t.TempDir() + "/receivers.json")
	if err != nil {
		t.Fatalf("LoadReceiverStore() error = %v", err)
	}
	digest, err := ParseTokenDigest(adminToken)
	if err != nil {
		t.Fatalf("ParseTokenDigest() error = %v", err)
	}
	hub := newTestHubConfigured(t, nil, func(cfg *ServerConfig) {
		cfg.AdminToken = digest
		cfg.ReceiverStore = store
		cfg.AdminPage = []byte("<!doctype html><title>RelayMic 管理</title>")
	})
	return hub, store, adminToken
}

func adminRequest(t *testing.T, method, url, body, bearer string) *http.Request {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

// 没配 admin 凭据就不该有管理面：公网上的 Hub 不能默认开一个能发凭据的口子。
func TestAdminSurfaceIsAbsentWithoutCredentials(t *testing.T) {
	hub := newTestHub(t)
	for _, path := range []string{"/admin", "/admin/api/receivers"} {
		res, err := http.Get(hub.http.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s = %d，没有 admin 凭据时应当 404", path, res.StatusCode)
		}
	}
}

func TestAdminApiNeedsTheAdminToken(t *testing.T) {
	hub, _, adminToken := newAdminHub(t)

	res, err := http.DefaultClient.Do(adminRequest(t, http.MethodGet, hub.http.URL+"/admin/api/receivers", "", ""))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("不带凭据 = %d，want 401", res.StatusCode)
	}

	res, err = http.DefaultClient.Do(adminRequest(t, http.MethodGet, hub.http.URL+"/admin/api/receivers", "", "wrong-token"))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错凭据 = %d，want 401", res.StatusCode)
	}

	res, err = http.DefaultClient.Do(adminRequest(t, http.MethodGet, hub.http.URL+"/admin/api/receivers", "", adminToken))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("对凭据 = %d，want 200", res.StatusCode)
	}
}

// 页面走会话 cookie：浏览器里不该把管理凭据留在 JS 手上。
func TestAdminLoginSetsASessionCookie(t *testing.T) {
	hub, _, adminToken := newAdminHub(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar}

	res, err := client.Do(adminRequest(t, http.MethodPost, hub.http.URL+"/admin/login", `{"token":"wrong"}`, ""))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错口令登录 = %d，want 401", res.StatusCode)
	}

	res, err = client.Do(adminRequest(t, http.MethodPost, hub.http.URL+"/admin/login", `{"token":"`+adminToken+`"}`, ""))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("登录 = %d，want 204", res.StatusCode)
	}

	res, err = client.Get(hub.http.URL + "/admin/api/receivers")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("带会话访问 = %d，want 200", res.StatusCode)
	}
}

// 这条是整件事的重点：在管理面加一台接收端，它当场就能连进来，不用重启 Hub。
func TestAdminCreatesAReceiverThatConnectsWithoutARestart(t *testing.T) {
	hub, store, adminToken := newAdminHub(t)

	res, err := http.DefaultClient.Do(adminRequest(t, http.MethodPost, hub.http.URL+"/admin/api/receivers", `{"name":"别人的电脑"}`, adminToken))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("新建 = %d，want 201", res.StatusCode)
	}
	var created struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Token == "" || created.Name != "别人的电脑" {
		t.Fatalf("新建返回 = %+v", created)
	}
	if len(store.List()) != 1 {
		t.Fatalf("清单里没有落盘：%v", store.List())
	}

	// 用刚发的 token 连进来，应当立刻拿到配对码。
	receiver, _, err := hub.dialReceiver(t, created.Token)
	if err != nil {
		t.Fatalf("dialReceiver() error = %v", err)
	}
	msg := receiver.recv()
	if msg.Type != TypeWaiting || len(msg.Code) != PairingCodeDigits {
		t.Fatalf("新接收端拿到 %+v，want waiting + 6 位码", msg)
	}
}

// 注销要当场生效：连接被掐断，token 也不再通过认证。
func TestAdminRevokeClosesTheLiveConnection(t *testing.T) {
	hub, _, adminToken := newAdminHub(t)

	res, err := http.DefaultClient.Do(adminRequest(t, http.MethodPost, hub.http.URL+"/admin/api/receivers", `{"name":"临时的人"}`, adminToken))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	res.Body.Close()

	receiver, _, err := hub.dialReceiver(t, created.Token)
	if err != nil {
		t.Fatalf("dialReceiver() error = %v", err)
	}
	receiver.recv()

	res, err = http.DefaultClient.Do(adminRequest(t, http.MethodDelete, hub.http.URL+"/admin/api/receivers/"+created.ID, "", adminToken))
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("注销 = %d，want 204", res.StatusCode)
	}

	receiver.expectClosed()
	if _, _, err := hub.dialReceiver(t, created.Token); err == nil {
		t.Fatal("注销之后 token 还能连进来")
	}
}

// 运维页要看得见谁在线。
func TestAdminListShowsWhoIsOnline(t *testing.T) {
	hub, _, adminToken := newAdminHub(t)
	receiver, _, err := hub.dialReceiver(t, hub.token)
	if err != nil {
		t.Fatalf("dialReceiver() error = %v", err)
	}
	receiver.recv()

	res, err := http.DefaultClient.Do(adminRequest(t, http.MethodGet, hub.http.URL+"/admin/api/receivers", "", adminToken))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	var view struct {
		Receivers []struct {
			Name   string `json:"name"`
			Online bool   `json:"online"`
		} `json:"receivers"`
	}
	if err := json.NewDecoder(res.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.Receivers) != 1 || view.Receivers[0].Name != "公司电脑" || !view.Receivers[0].Online {
		t.Fatalf("列表 = %+v，want 公司电脑 在线", view.Receivers)
	}
}

// 登录口在公网上，得挡住暴力试。
func TestAdminLoginIsRateLimited(t *testing.T) {
	hub, _, _ := newAdminHub(t)
	last := 0
	for i := 0; i < 12; i++ {
		res, err := http.DefaultClient.Do(adminRequest(t, http.MethodPost, hub.http.URL+"/admin/login", `{"token":"wrong"}`, ""))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		res.Body.Close()
		last = res.StatusCode
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("连续错口令之后 = %d，want 429", last)
	}
}

// 会话不该永远有效。
func TestAdminSessionExpires(t *testing.T) {
	hub, _, adminToken := newAdminHub(t)
	clock := &fakeClock{t: time.Now()}
	hub.server.admin.now = clock.now

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	res, err := client.Do(adminRequest(t, http.MethodPost, hub.http.URL+"/admin/login", `{"token":"`+adminToken+`"}`, ""))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()

	clock.advance(adminSessionTTL + time.Minute)
	res, err = client.Get(hub.http.URL + "/admin/api/receivers")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("过期会话 = %d，want 401", res.StatusCode)
	}
}

var _ = httptest.NewServer

// 清单写不进去是部署问题（unit 没给可写目录），不是"名字填错了"：
// 回 500，而且正文要说清该改哪儿。
func TestAdminMintFailsWith500WhenTheStoreIsNotWritable(t *testing.T) {
	adminToken := mustNewToken(t)
	store, err := LoadReceiverStore(unwritableStorePath(t))
	if err != nil {
		t.Fatalf("LoadReceiverStore() error = %v", err)
	}
	digest, err := ParseTokenDigest(adminToken)
	if err != nil {
		t.Fatalf("ParseTokenDigest() error = %v", err)
	}
	hub := newTestHubConfigured(t, nil, func(cfg *ServerConfig) {
		cfg.AdminToken = digest
		cfg.ReceiverStore = store
		cfg.AdminPage = []byte("<!doctype html><title>RelayMic 管理</title>")
	})

	res, err := http.DefaultClient.Do(adminRequest(t, http.MethodPost,
		hub.http.URL+"/admin/api/receivers", `{"name":"别人的电脑"}`, adminToken))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("落盘失败 = %d，want 500", res.StatusCode)
	}
	if !strings.Contains(string(body), "StateDirectory=relaymic") {
		t.Fatalf("正文没说清怎么修: %s", body)
	}
}
