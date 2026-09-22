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
			return err
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
