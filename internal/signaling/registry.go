package signaling

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	// ErrPairingFailed 是发送端能看到的唯一配对失败原因。
	//
	// 未知码、过期码、已用码、被限流全都返回它：一旦区分开，
	// 响应差异本身就成了枚举预言机，攻击者可以先筛出"存在但过期"的码。
	ErrPairingFailed = errors.New("配对失败")
	// ErrInvalidPairingCode 是本地输入格式错误，只用于日志，不发给对端。
	ErrInvalidPairingCode = errors.New("配对码格式错误")
	// ErrUnknownReceiver 表示配置里没有这台接收端，属于运维错误。
	ErrUnknownReceiver = errors.New("未配置的接收端")
)

// DefaultMaxAttemptsPerMinute 是每个客户端每分钟允许的失败配对次数。
const DefaultMaxAttemptsPerMinute = 5

// ReceiverConfig 是配置里的一台接收端。
// Token 只在进程启动时读入，转成摘要后明文立即丢弃。
type ReceiverConfig struct {
	Name  string `json:"name"`
	Token string `json:"token"`
}

// Config 是 Registry 的构造参数；零值字段走默认。
type Config struct {
	Receivers            []ReceiverConfig
	CodeTTL              time.Duration
	MaxAttemptsPerMinute int
	Now                  func() time.Time
	NewCode              func() (PairingCode, error)
}

// Receiver 是已认证的接收端身份。
type Receiver struct {
	id     string
	name   string
	digest TokenDigest
}

// ID 是进程内标识，与 token 无关，也不参与对外协议。
func (r *Receiver) ID() string { return r.id }

// Name 是展示给发送端的机器名。
func (r *Receiver) Name() string { return r.name }

type codeEntry struct {
	code      PairingCode
	expiresAt time.Time
}

// Registry 保存公网控制面的全部权威状态：谁持有哪个 token、当前有哪些配对码。
//
// 它完全不碰网络，因此可以在没有连接的情况下把配对语义测干净 ——
// 竞态、过期、限流这些最容易出错的地方不该藏在 WebSocket 后面。
type Registry struct {
	mu        sync.Mutex
	codeTTL   time.Duration
	maxPerMin int
	now       func() time.Time
	newCode   func() (PairingCode, error)

	byID   map[string]*Receiver
	live   map[string]codeEntry
	limits map[string][]time.Time
}

// NewRegistry 读取接收端清单并校验配置。
func NewRegistry(cfg Config) (*Registry, error) {
	r := &Registry{
		codeTTL:   cfg.CodeTTL,
		maxPerMin: cfg.MaxAttemptsPerMinute,
		now:       cfg.Now,
		newCode:   cfg.NewCode,
		byID:      make(map[string]*Receiver),
		live:      make(map[string]codeEntry),
		limits:    make(map[string][]time.Time),
	}
	if r.codeTTL <= 0 {
		r.codeTTL = PairingCodeTTL
	}
	if r.maxPerMin <= 0 {
		r.maxPerMin = DefaultMaxAttemptsPerMinute
	}
	if r.now == nil {
		r.now = time.Now
	}
	if r.newCode == nil {
		r.newCode = NewPairingCode
	}

	seen := make(map[string]bool, len(cfg.Receivers))
	for _, rc := range cfg.Receivers {
		if rc.Name == "" {
			return nil, errors.New("接收端缺少名字")
		}
		if seen[rc.Name] {
			return nil, fmt.Errorf("接收端名字重复: %s", rc.Name)
		}
		seen[rc.Name] = true
		digest, err := ParseTokenDigest(rc.Token)
		if err != nil {
			return nil, fmt.Errorf("接收端 %s: %w", rc.Name, err)
		}
		id, err := newReceiverID()
		if err != nil {
			return nil, err
		}
		r.byID[id] = &Receiver{id: id, name: rc.Name, digest: digest}
	}
	return r, nil
}

func newReceiverID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成接收端标识: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// Authenticate 验证接收端 token。
//
// 遍历全部配置项且不提前返回：耗时只跟"配了几台"有关，
// 跟"匹配到第几台"无关。
func (r *Registry) Authenticate(token string) (*Receiver, bool) {
	if token == "" {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var found *Receiver
	for _, rec := range r.byID {
		if TokenMatches(rec.digest, token) {
			found = rec
		}
	}
	return found, found != nil
}

// IssueCode 给一台接收端发新码，并作废它上一个码。
// 接收端每次重连、每个会话结束都会走这里，所以"同时只有一个活码"是硬约束。
func (r *Registry) IssueCode(receiverID string) (PairingCode, time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[receiverID]; !ok {
		return "", time.Time{}, ErrUnknownReceiver
	}

	var code PairingCode
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		code, err = r.newCode()
		if err != nil {
			return "", time.Time{}, err
		}
		// 撞上别的接收端的活码就重抽：两个房间同码会让发送端连错机器。
		if !r.codeUsedByOther(receiverID, code) {
			break
		}
	}
	expiresAt := r.now().Add(r.codeTTL)
	r.live[receiverID] = codeEntry{code: code, expiresAt: expiresAt}
	return code, expiresAt, nil
}

// CodeTTL 是配对码的有效期，供 Hub 告诉客户端"这个码还能用多久"。
func (r *Registry) CodeTTL() time.Duration { return r.codeTTL }

// Add 在运行时登记一台接收端。
//
// 这条路径让"加一个人"不用重启 Hub —— 重启会掐断正在通话的人，而那个动作
// 本来和已有的人无关。
func (r *Registry) Add(rec ReceiverRecord) (*Receiver, error) {
	if rec.ID == "" || rec.Name == "" {
		return nil, errors.New("接收端记录缺少 id 或名字")
	}
	digest, err := decodeDigest(rec.Digest)
	if err != nil {
		return nil, fmt.Errorf("接收端 %s: %w", rec.Name, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[rec.ID]; ok {
		return nil, fmt.Errorf("接收端 %s 已经在册", rec.ID)
	}
	for _, other := range r.byID {
		if other.name == rec.Name {
			return nil, fmt.Errorf("已经有一台叫 %q 的接收端", rec.Name)
		}
	}
	added := &Receiver{id: rec.ID, name: rec.Name, digest: digest}
	r.byID[rec.ID] = added
	return added, nil
}

// Remove 注销一台接收端：身份和它的活码一起作废。
//
// 留着码等于"删了人还能连进来"，所以两件事必须一起做。
func (r *Registry) Remove(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[id]; !ok {
		return false
	}
	delete(r.byID, id)
	delete(r.live, id)
	return true
}

// Receivers 列出全部接收端，按名字排序，供运维页面展示。
func (r *Registry) Receivers() []Receiver {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Receiver, 0, len(r.byID))
	for _, rec := range r.byID {
		out = append(out, *rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// LiveCode 返回某台接收端当前的活码和它的到期时间。
//
// 只读，不改状态：Hub 用它判断"这张码是不是该换一张了"。
func (r *Registry) LiveCode(receiverID string) (PairingCode, time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.live[receiverID]
	if !ok {
		return "", time.Time{}, false
	}
	return entry.code, entry.expiresAt, true
}

func (r *Registry) codeUsedByOther(receiverID string, code PairingCode) bool {
	for id, entry := range r.live {
		if id == receiverID {
			continue
		}
		if EqualPairingCode(entry.code, code) {
			return true
		}
	}
	return false
}

// Redeem 用配对码换取接收端身份。成功即消费该码，失败按客户端计次。
func (r *Registry) Redeem(client string, code PairingCode) (*Receiver, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	if len(r.pruneFailures(client, now)) >= r.maxPerMin {
		return nil, ErrPairingFailed
	}

	var matched *Receiver
	for id, entry := range r.live {
		if !EqualPairingCode(entry.code, code) {
			continue
		}
		if !now.Before(entry.expiresAt) {
			continue
		}
		matched = r.byID[id]
		delete(r.live, id)
		break
	}
	if matched == nil {
		r.limits[client] = append(r.limits[client], now)
		return nil, ErrPairingFailed
	}
	// 配对成功说明这是合法用户，把之前的失败计数清掉。
	delete(r.limits, client)
	return matched, nil
}

// Release 作废某台接收端当前的配对码，用于接收端断线。
// 身份本身保留：同一 token 重连后必须还能拿到新码。
func (r *Registry) Release(receiverID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.live, receiverID)
}

func (r *Registry) pruneFailures(client string, now time.Time) []time.Time {
	kept := r.limits[client][:0]
	for _, ts := range r.limits[client] {
		if now.Sub(ts) < time.Minute {
			kept = append(kept, ts)
		}
	}
	r.limits[client] = kept
	return kept
}
