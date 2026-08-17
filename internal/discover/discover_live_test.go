package discover

import "testing"

// 真实环境探针：手动跑 go test -run TestLiveDiscover -v 用。
func TestLiveDiscover(t *testing.T) {
	if testing.Short() {
		t.Skip("live 探针")
	}
	peers, err := tailnetPeers()
	t.Logf("peers: %v err: %v", peers, err)
	found, err := Receivers()
	t.Logf("found: %v err: %v", found, err)
}

func TestLiveSelfName(t *testing.T) {
	if testing.Short() {
		t.Skip("live 探针")
	}
	t.Logf("DNS: %q  CLI: %q", selfNameFromDNS(), selfNameFromCLI())
}
