package signaling

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*ReceiverStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "receivers.json")
	store, err := LoadReceiverStore(path)
	if err != nil {
		t.Fatalf("LoadReceiverStore() error = %v", err)
	}
	return store, path
}

// 文件不存在就是"还没加过人"，不是错误：全新部署的 Hub 也该能直接起来。
func TestReceiverStoreStartsEmptyWhenTheFileIsMissing(t *testing.T) {
	store, _ := newTestStore(t)
	if got := store.List(); len(got) != 0 {
		t.Fatalf("List() = %v，want empty", got)
	}
}

// 落盘只留摘要：这份文件被读走也不等于交出可用凭据。
func TestReceiverStorePersistsDigestsNotTokens(t *testing.T) {
	store, path := newTestStore(t)
	rec, token, err := store.Add("别人的电脑", time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if rec.Name != "别人的电脑" || rec.ID == "" {
		t.Fatalf("Add() = %+v", rec)
	}
	if !TokenMatches(mustDigest(t, rec.Digest), token) {
		t.Fatal("返回的 token 和记录里的摘要对不上")
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	if strings.Contains(string(body), token) {
		t.Fatal("清单文件里出现了明文 token")
	}

	// 重新加载后仍然能认出这个 token。
	again, err := LoadReceiverStore(path)
	if err != nil {
		t.Fatalf("LoadReceiverStore() error = %v", err)
	}
	recs := again.List()
	if len(recs) != 1 || recs[0].ID != rec.ID || recs[0].Digest != rec.Digest {
		t.Fatalf("重载后 = %+v，want %+v", recs, rec)
	}
}

// 名字是运维区分人的唯一手段，重名必须当场拒绝。
func TestReceiverStoreRejectsDuplicateNames(t *testing.T) {
	store, _ := newTestStore(t)
	if _, _, err := store.Add("公司电脑", time.Now()); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, _, err := store.Add("公司电脑", time.Now()); err == nil {
		t.Fatal("Add() 放过了重名")
	}
	if _, _, err := store.Add("  ", time.Now()); err == nil {
		t.Fatal("Add() 放过了空名字")
	}
}

func TestReceiverStoreRemoveDropsTheRecordForGood(t *testing.T) {
	store, path := newTestStore(t)
	rec, _, err := store.Add("临时的人", time.Now())
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := store.Remove(rec.ID); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if got := store.List(); len(got) != 0 {
		t.Fatalf("Remove() 之后还有 %v", got)
	}
	if err := store.Remove(rec.ID); err == nil {
		t.Fatal("删不存在的记录没有报错")
	}
	again, err := LoadReceiverStore(path)
	if err != nil {
		t.Fatalf("LoadReceiverStore() error = %v", err)
	}
	if len(again.List()) != 0 {
		t.Fatal("删除没有落盘")
	}
}

func mustDigest(t *testing.T, encoded string) TokenDigest {
	t.Helper()
	d, err := decodeDigest(encoded)
	if err != nil {
		t.Fatalf("decodeDigest(%q) error = %v", encoded, err)
	}
	return d
}

// unwritableStorePath 造一个写不进去的清单路径：把父目录做成一个普通文件。
// 不依赖 chmod，Windows 上也一样会失败。
func unwritableStorePath(t *testing.T) string {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	return filepath.Join(blocker, "receivers.json")
}

// 全新部署的机器上清单目录还不存在，启动检查要顺手建出来。
func TestReceiverStoreVerifyWritableCreatesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	store, err := LoadReceiverStore(filepath.Join(dir, "receivers.json"))
	if err != nil {
		t.Fatalf("LoadReceiverStore() error = %v", err)
	}
	if err := store.VerifyWritable(); err != nil {
		t.Fatalf("VerifyWritable() error = %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("目录没建出来: %v", err)
	}
	// 探针只探一次，不能留下垃圾：saveLocked 用的就是这个名字。
	if _, err := os.Stat(store.path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("探针临时文件没清掉: %v", err)
	}
}

// 生产上踩过的那一次：unit 里 ProtectSystem=strict，/var/lib 对服务只读。
// 启动就得失败，并且要说清该改哪儿 —— 不然要等到有人生成 token 才看见一句 errno。
func TestReceiverStoreVerifyWritableReportsAnUnwritablePath(t *testing.T) {
	store, err := LoadReceiverStore(unwritableStorePath(t))
	if err != nil {
		t.Fatalf("LoadReceiverStore() error = %v", err)
	}
	err = store.VerifyWritable()
	if !errors.Is(err, ErrStoreNotWritable) {
		t.Fatalf("VerifyWritable() error = %v，want ErrStoreNotWritable", err)
	}
	if !strings.Contains(err.Error(), "StateDirectory=relaymic") {
		t.Fatalf("报错没说清怎么修: %v", err)
	}
}

// 落盘失败也要归到 ErrStoreNotWritable：管理面靠它决定回 500 还是 400。
func TestReceiverStoreAddReportsAnUnwritablePath(t *testing.T) {
	store, err := LoadReceiverStore(unwritableStorePath(t))
	if err != nil {
		t.Fatalf("LoadReceiverStore() error = %v", err)
	}
	if _, _, err := store.Add("别人的电脑", time.Now()); !errors.Is(err, ErrStoreNotWritable) {
		t.Fatalf("Add() error = %v，want ErrStoreNotWritable", err)
	}
	if got := store.List(); len(got) != 0 {
		t.Fatalf("写失败之后内存里还留着记录: %v", got)
	}
}

// 动态加进来的接收端要能立刻用它的 token 通过认证：加人不该等于重启 Hub。
func TestRegistryAcceptsADynamicallyAddedReceiver(t *testing.T) {
	reg, _, _ := newTestRegistry(t, Config{})
	token := mustNewToken(t)
	rec := ReceiverRecord{ID: "dyn-1", Name: "新的人", Digest: encodeDigest(DigestToken(token))}

	added, err := reg.Add(rec)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if added.Name() != "新的人" || added.ID() != "dyn-1" {
		t.Fatalf("Add() = %s/%s", added.ID(), added.Name())
	}
	got, ok := reg.Authenticate(token)
	if !ok || got.ID() != "dyn-1" {
		t.Fatalf("新加的接收端认证不过：%v", ok)
	}
}

// 名字是运维区分人的唯一手段，重名要当场拒绝，而不是让两台机器抢同一个名字。
func TestRegistryRejectsADuplicateName(t *testing.T) {
	reg, _, _ := newTestRegistry(t, Config{})
	token := mustNewToken(t)
	if _, err := reg.Add(ReceiverRecord{ID: "dyn-2", Name: "公司电脑", Digest: encodeDigest(DigestToken(token))}); err == nil {
		t.Fatal("Add() 放过了重名")
	}
}

// 注销要连活码一起作废：留着码等于"删了人还能连进来"。
func TestRegistryRemoveRevokesTheTokenAndItsLiveCode(t *testing.T) {
	reg, _, _ := newTestRegistry(t, Config{})
	token := mustNewToken(t)
	if _, err := reg.Add(ReceiverRecord{ID: "dyn-3", Name: "临时的人", Digest: encodeDigest(DigestToken(token))}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, _, err := reg.IssueCode("dyn-3"); err != nil {
		t.Fatalf("IssueCode() error = %v", err)
	}

	if !reg.Remove("dyn-3") {
		t.Fatal("Remove() 说没删掉")
	}
	if _, ok := reg.Authenticate(token); ok {
		t.Fatal("注销之后 token 还能用")
	}
	if _, _, ok := reg.LiveCode("dyn-3"); ok {
		t.Fatal("注销之后还留着活码")
	}
	if reg.Remove("dyn-3") {
		t.Fatal("重复删除返回了 true")
	}
}

func TestRegistryListsEveryReceiver(t *testing.T) {
	reg, _, _ := newTestRegistry(t, Config{})
	if _, err := reg.Add(ReceiverRecord{ID: "dyn-4", Name: "别人的电脑", Digest: encodeDigest(DigestToken(mustNewToken(t)))}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	got := reg.Receivers()
	if len(got) != 2 {
		t.Fatalf("Receivers() 有 %d 台，want 2", len(got))
	}
	// 顺序是稳定的字节序（中文不按拼音），列表页要的只是"每次都一样"。
	if got[0].Name() != "公司电脑" || got[1].Name() != "别人的电脑" {
		t.Fatalf("Receivers() = %s, %s（应当按名字排序）", got[0].Name(), got[1].Name())
	}
}

func mustNewToken(t *testing.T) string {
	t.Helper()
	token, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	return token
}
