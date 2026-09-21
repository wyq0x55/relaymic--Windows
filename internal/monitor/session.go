package monitor

import "sync"

// SessionState 记住当前正在进行的会话，供本机控制台"断开当前通话"使用。
// 它只保存 Hub 下发的会话标识，不含任何凭据。
type SessionState struct {
	mu sync.Mutex
	id string
}

// NewSessionState 构造一个空会话状态。
func NewSessionState() *SessionState { return &SessionState{} }

// Set 记录当前会话。
func (s *SessionState) Set(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.id = id
}

// Clear 只清掉指定的那条会话：收尾别人的会话不能把当前会话一起抹掉。
func (s *SessionState) Clear(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.id == id {
		s.id = ""
	}
}

// Get 返回当前会话；没有会话时为空。
func (s *SessionState) Get() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}
