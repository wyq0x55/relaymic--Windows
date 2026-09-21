package signaling

import "encoding/json"

// MaxMessageBytes 是单条控制面消息的上限。
//
// 控制面只搬 SDP，最大的那种也就几 KB。给 64 KiB 是留足余量后仍然
// 让"往信令通道灌大包"这条路直接撞墙。
const MaxMessageBytes = 64 << 10

// MaxPairingAttemptsPerConn 是单条发送端连接允许的失败配对次数。
// 超过就断开，把在线穷举的速率压到跟重连开销同级。
const MaxPairingAttemptsPerConn = 3

// 控制面消息类型。协议只有这一组，没有"转发给任意目标"这种口子。
const (
	// Hub -> Receiver：新配对码，等待发送端。
	TypeWaiting = "waiting"
	// Hub -> Receiver：发送端已用码接入。
	TypeJoined = "joined"
	// Hub -> Receiver：发送端离开，会话结束。
	TypeLeft = "left"
	// Sender -> Hub -> Receiver：完整 SDP offer。
	TypeOffer = "offer"
	// Receiver -> Hub -> Sender：完整 SDP answer。
	TypeAnswer = "answer"
	// Sender -> Hub：用配对码认领接收端。
	TypePair = "pair"
	// Hub -> Sender：配对成功。
	TypePaired = "paired"
	// Hub -> 任一端：配对失败等可展示错误。
	TypeError = "error"
	// Hub -> Sender：接收端离线，会话不可继续。
	TypeClosed = "closed"
)

// 对外错误文案。
const (
	// PairingFailedText 是配对失败时唯一的对外说法。
	PairingFailedText = "配对失败：码不对、已过期或已用过"
	// ReceiverOfflineText 表示接收端已经断开。
	ReceiverOfflineText = "接收端已离线"
)

// Message 是控制面上的唯一信封。
//
// SDP 保持原始 JSON：控制面只做搬运，不改写、不解析、不缓存 ——
// 一旦开始"理解" SDP，就得跟着 WebRTC 的每个版本改，且多一处出错的地方。
type Message struct {
	Type      string          `json:"type"`
	Session   string          `json:"session,omitempty"`
	SDP       json.RawMessage `json:"sdp,omitempty"`
	Code      string          `json:"code,omitempty"`
	Name      string          `json:"name,omitempty"`
	ExpiresIn int             `json:"expiresInSec,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// ICEServer 是发给浏览器的 ICE 配置，形状对齐浏览器 RTCPeerConnection 的字段名。
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}
