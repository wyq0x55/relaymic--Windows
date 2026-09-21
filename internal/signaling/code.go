package signaling

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// PairingCode 是发送端认领一台接收端用的短期凭据。
//
// 它必须不可预测：猜到码就等于把音频注进别人电脑的虚拟麦克风。
// 所以码由 crypto/rand 生成、比较走常数时间、且只能兑换一次。
type PairingCode string

const (
	// PairingCodeDigits 是配对码位数。6 位十进制在 5 分钟有效期加限流下，
	// 穷举成功的概率可以忽略，同时短到能在手机上手动敲进去。
	PairingCodeDigits = 6
	// PairingCodeTTL 是配对码的有效期。到期即作废，不续期。
	PairingCodeTTL = 5 * time.Minute
)

// codeSpace 是 10^PairingCodeDigits。
var codeSpace = func() *big.Int {
	n := big.NewInt(1)
	for i := 0; i < PairingCodeDigits; i++ {
		n.Mul(n, big.NewInt(10))
	}
	return n
}()

// NewPairingCode 生成一个十进制配对码。
//
// 用 crypto/rand.Int 而不是 rand.N 或取模：前者在 [0, 10^digits) 上均匀，
// 不会因为取模把低位码的概率抬高。
func NewPairingCode() (PairingCode, error) {
	n, err := rand.Int(rand.Reader, codeSpace)
	if err != nil {
		return "", fmt.Errorf("生成配对码: %w", err)
	}
	digits := n.String()
	if pad := PairingCodeDigits - len(digits); pad > 0 {
		digits = strings.Repeat("0", pad) + digits
	}
	return PairingCode(digits), nil
}

// NormalizePairingCode 把用户输入收敛成规范形式，只接受 ASCII 十进制数字。
//
// 空格和连字符是输入习惯，丢掉；全角数字、字母、长度不符一律拒绝 ——
// 不做"猜用户想输什么"的容错，容错面就是攻击面。
func NormalizePairingCode(in string) (PairingCode, error) {
	var b strings.Builder
	b.Grow(len(in))
	for _, r := range in {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '\t' || r == '-' || r == '\u00a0':
			// 分隔符不参与匹配。
		default:
			return "", fmt.Errorf("%w: 只能是 %d 位数字", ErrInvalidPairingCode, PairingCodeDigits)
		}
	}
	out := PairingCode(b.String())
	if len(out) != PairingCodeDigits {
		return "", fmt.Errorf("%w: 只能是 %d 位数字", ErrInvalidPairingCode, PairingCodeDigits)
	}
	return out, nil
}

// EqualPairingCode 常数时间比较两个配对码。
// 长度不同时 ConstantTimeCompare 直接返回 0，不泄露任何一位。
func EqualPairingCode(a, b PairingCode) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
