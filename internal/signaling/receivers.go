package signaling

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ReceiverRecord 是一条动态添加的接收端记录。
//
// 只存 token 的摘要：这份清单被读走也不等于交出可用凭据。明文只在创建的那一刻
// 返回一次，之后 Hub 自己也拿不回来。
type ReceiverRecord struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Digest    string    `json:"tokenDigest"`
	CreatedAt time.Time `json:"createdAt"`
}

// ReceiverStore 是动态接收端清单，落盘成一份 JSON。
//
// 它存在的理由：加一个人不该等于"改服务器配置 + 重启 Hub"。重启会掐断正在
// 通话的人，而这个动作本来和已有的人无关。
type ReceiverStore struct {
	path string

	mu   sync.Mutex
	recs []ReceiverRecord
}

// ErrStoreNotWritable 说明清单所在的目录写不进去。
//
// 生产上踩过一次：unit 里有 ProtectSystem=strict，/var/lib 对服务是只读的，
// 于是"生成 token"在页面上报一句 read-only file system。这是部署问题，不是
// 请求问题，所以单独分类，好让调用方回 500 并在启动时就先报出来。
var ErrStoreNotWritable = errors.New("接收端清单写不进去")

// LoadReceiverStore 读取清单。文件不存在就是"还没加过人"，不是错误。
func LoadReceiverStore(path string) (*ReceiverStore, error) {
	s := &ReceiverStore{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	// 拼错的键直接失败：清单是 Hub 自己写的，出现不认识的键说明被人改坏了。
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&s.recs); err != nil {
		return nil, fmt.Errorf("接收端清单 %s: %w", path, err)
	}
	for _, rec := range s.recs {
		if _, err := decodeDigest(rec.Digest); err != nil {
			return nil, fmt.Errorf("接收端清单 %s: %s 的摘要不合法: %w", path, rec.Name, err)
		}
	}
	return s, nil
}

// VerifyWritable 在启动时确认这份清单真的能落盘。
//
// 存在的理由：目录不可写只有等到有人在管理面上生成 token 时才暴露，报出来的
// 是一句 errno，看不出该改哪儿。启动就失败，日志里直接指到路径和该加的那行
// unit 配置。只在启动时调用：它写的就是 saveLocked 用的那个临时文件。
func (s *ReceiverStore) VerifyWritable() error {
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return s.notWritableError(err)
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, []byte("{}\n"), 0o600); err != nil {
		return s.notWritableError(err)
	}
	if err := os.Remove(tmp); err != nil {
		return s.notWritableError(err)
	}
	return nil
}

// notWritableError 把 errno 翻成"该改哪儿"：只给路径和原因，日志里够定位了。
func (s *ReceiverStore) notWritableError(cause error) error {
	return fmt.Errorf("%w: %s: %v；systemd 下给 unit 加 StateDirectory=relaymic，或把 receiversFile 指到可写目录",
		ErrStoreNotWritable, s.path, cause)
}

// List 返回当前全部记录。
func (s *ReceiverStore) List() []ReceiverRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ReceiverRecord, len(s.recs))
	copy(out, s.recs)
	return out
}

// Add 新建一条记录，返回记录和明文 token —— 明文只在这里出现一次。
func (s *ReceiverStore) Add(name string, now time.Time) (ReceiverRecord, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ReceiverRecord{}, "", errors.New("接收端名字不能为空")
	}

	token, err := NewToken()
	if err != nil {
		return ReceiverRecord{}, "", err
	}
	id, err := newReceiverID()
	if err != nil {
		return ReceiverRecord{}, "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.recs {
		if rec.Name == name {
			return ReceiverRecord{}, "", fmt.Errorf("已经有一台叫 %q 的接收端", name)
		}
	}
	rec := ReceiverRecord{
		ID:        id,
		Name:      name,
		Digest:    encodeDigest(DigestToken(token)),
		CreatedAt: now,
	}
	s.recs = append(s.recs, rec)
	if err := s.saveLocked(); err != nil {
		s.recs = s.recs[:len(s.recs)-1]
		return ReceiverRecord{}, "", err
	}
	return rec, token, nil
}

// Remove 删除一条记录。
func (s *ReceiverStore) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, rec := range s.recs {
		if rec.ID != id {
			continue
		}
		kept := append(append([]ReceiverRecord{}, s.recs[:i]...), s.recs[i+1:]...)
		s.recs = kept
		if err := s.saveLocked(); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("没有 id 为 %q 的接收端", id)
}

// saveLocked 原子写入：先写临时文件再改名，中途失败不会留下半份清单。
func (s *ReceiverStore) saveLocked() error {
	data, err := json.MarshalIndent(s.recs, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return s.notWritableError(err)
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return s.notWritableError(err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return s.notWritableError(err)
	}
	return nil
}
