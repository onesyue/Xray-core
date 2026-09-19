package strmatcher_test

// 这组测试守的是一次纯内存表示改动：MphMatcherGroup 的 `rules []string` 与
// `values [][]uint32` 被换成连续 blob + CSR 索引。**判定语义一个字都不能变**，
// 而它的失败模式是「路由静默错判、零报错」—— `Lookup` 跑在每连接每个 `.` 上。
//
// 所以这里不测「新实现自己自洽」，而是把改动前那份实现**逐字复制**进来当参照物
// （`legacyMphMatcherGroup`），用**同一份生产 geosite.dat** 各建一个，对十万量级
// 的真实语料逐条比对 Lookup / Match / MatchAny 的结果，包括命中值本身。
//
// 参照物是「被这次改动替换掉的那段代码」，不是我重新发明的等价物 —— 这一点是
// guard-reimplements-its-subject 那条教训的反面：差分测试的参照物必须是旧的真东西。

import (
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/xtls/xray-core/common/geodata"
	"github.com/xtls/xray-core/common/geodata/strmatcher"
)

// 扫描地板：低于这些数说明 geosite.dat 被换小了、或解析掉了东西。
// 「比对全过」在一个空语料上同样成立，所以必须先证明真的扫到了东西。
// 仓内 resources/geosite.dat 实测：109,231 条目 / 38,355 个唯一 pattern。
const (
	uniquePatternFloor = 30000
	mphRuleFloor       = 60000
	matchHitFloor      = 30000
)

// 内存判据刻意用**比值**而不是绝对字节数：绝对值随语料规模变，比值不变。
// 紧凑化拿掉的是每条规则的 slice header（values 24B + rules 16B）与每条字符串的
// 分配器取整，所以期望比值落在 0.4~0.5。0.6 是留了余量的天花板。
const compactionRatioCeiling = 0.60

// 允许把同一条守卫指到另一份 .dat（例如生产那份 11 MB 的）做一次性实测。
// 不设时用仓内那份 —— 缺省路径必须存在，否则这条守卫在 CI 里就是一条没有合法出口的红灯。
const geositeEnvVar = "YUE_GEOSITE_DAT"

// ---------------------------------------------------------------------------
// 参照物：2026-09-20 紧凑化之前的 MphMatcherGroup，逐字复制。
// 只用 strmatcher 的导出 API（RollingHash / MemHash / Type / CompositeMatchesReverse），
// nextPow2 是纯算术辅助、不是被测对象，就地重写。
// ---------------------------------------------------------------------------

type legacyRuleInfo struct {
	rollingHash uint32
	matchers    [2][]uint32 // 下标是 strmatcher.Full(0) / strmatcher.Domain(1)
}

type legacyMphMatcherGroup struct {
	rules      []string
	values     [][]uint32
	level0     []uint32
	level0Mask uint32
	level1     []uint32
	level1Mask uint32
	ruleInfos  *map[string]legacyRuleInfo
}

func newLegacyMphMatcherGroup() *legacyMphMatcherGroup {
	return &legacyMphMatcherGroup{
		rules:     []string{""},
		values:    [][]uint32{nil},
		ruleInfos: &map[string]legacyRuleInfo{},
	}
}

func (g *legacyMphMatcherGroup) AddFullMatcher(matcher strmatcher.FullMatcher, value uint32) {
	pattern := strings.ToLower(matcher.Pattern())
	g.addPattern(0, "", pattern, matcher.Type(), value)
}

func (g *legacyMphMatcherGroup) AddDomainMatcher(matcher strmatcher.DomainMatcher, value uint32) {
	pattern := strings.ToLower(matcher.Pattern())
	hash := g.addPattern(0, "", pattern, matcher.Type(), value)
	g.addPattern(hash, pattern, ".", matcher.Type(), value)
}

func (g *legacyMphMatcherGroup) addPattern(suffixHash uint32, suffixPattern string, pattern string, matcherType strmatcher.Type, value uint32) uint32 {
	fullPattern := pattern + suffixPattern
	info, found := (*g.ruleInfos)[fullPattern]
	if !found {
		info = legacyRuleInfo{rollingHash: strmatcher.RollingHash(suffixHash, pattern)}
		g.rules = append(g.rules, fullPattern)
		g.values = append(g.values, nil)
	}
	info.matchers[matcherType] = append(info.matchers[matcherType], value)
	(*g.ruleInfos)[fullPattern] = info
	return info.rollingHash
}

func (g *legacyMphMatcherGroup) Build() error {
	ruleCount := len(*g.ruleInfos)
	g.level0 = make([]uint32, legacyNextPow2(ruleCount/4))
	g.level0Mask = uint32(len(g.level0) - 1)
	g.level1 = make([]uint32, legacyNextPow2(ruleCount))
	g.level1Mask = uint32(len(g.level1) - 1)

	buckets := make([][]uint32, len(g.level0))
	for ruleIdx := 1; ruleIdx < len(g.rules); ruleIdx++ {
		ruleInfo := (*g.ruleInfos)[g.rules[ruleIdx]]
		bucketIdx := ruleInfo.rollingHash & g.level0Mask
		buckets[bucketIdx] = append(buckets[bucketIdx], uint32(ruleIdx))
		g.values[ruleIdx] = append(ruleInfo.matchers[strmatcher.Full], ruleInfo.matchers[strmatcher.Domain]...) // nolint:gocritic
	}
	g.ruleInfos = nil
	runtime.GC()

	bucketIdxs := make([]int, len(buckets))
	for bucketIdx := range buckets {
		bucketIdxs[bucketIdx] = bucketIdx
	}
	sort.Slice(bucketIdxs, func(i, j int) bool { return len(buckets[bucketIdxs[i]]) > len(buckets[bucketIdxs[j]]) })

	occupied := make([]bool, len(g.level1))
	hashedBucket := make([]uint32, 0, 4)
	for _, bucketIdx := range bucketIdxs {
		bucket := buckets[bucketIdx]
		hashedBucket = hashedBucket[:0]
		seed := uint32(0)
		for len(hashedBucket) != len(bucket) {
			for _, ruleIdx := range bucket {
				memHash := strmatcher.MemHash(seed, g.rules[ruleIdx]) & g.level1Mask
				if occupied[memHash] {
					for _, hash := range hashedBucket {
						occupied[hash] = false
						g.level1[hash] = 0
					}
					hashedBucket = hashedBucket[:0]
					seed++
					break
				}
				occupied[memHash] = true
				g.level1[memHash] = ruleIdx
				hashedBucket = append(hashedBucket, memHash)
			}
		}
		g.level0[bucketIdx] = seed
	}
	return nil
}

func (g *legacyMphMatcherGroup) Lookup(rollingHash uint32, input string) uint32 {
	i0 := rollingHash & g.level0Mask
	seed := g.level0[i0]
	i1 := strmatcher.MemHash(seed, input) & g.level1Mask
	if n := g.level1[i1]; g.rules[n] == input {
		return n
	}
	return 0
}

func (g *legacyMphMatcherGroup) Match(input string) []uint32 {
	matches := make([][]uint32, 0, 5)
	hash := uint32(0)
	for i := len(input) - 1; i >= 0; i-- {
		hash = hash*strmatcher.PrimeRK + uint32(input[i])
		if input[i] == '.' {
			if mphIdx := g.Lookup(hash, input[i:]); mphIdx != 0 {
				matches = append(matches, g.values[mphIdx])
			}
		}
	}
	if mphIdx := g.Lookup(hash, input); mphIdx != 0 {
		matches = append(matches, g.values[mphIdx])
	}
	return strmatcher.CompositeMatchesReverse(matches)
}

func (g *legacyMphMatcherGroup) MatchAny(input string) bool {
	hash := uint32(0)
	for i := len(input) - 1; i >= 0; i-- {
		hash = hash*strmatcher.PrimeRK + uint32(input[i])
		if input[i] == '.' {
			if g.Lookup(hash, input[i:]) != 0 {
				return true
			}
		}
	}
	return g.Lookup(hash, input) != 0
}

func legacyNextPow2(v int) int {
	if v <= 1 {
		return 1
	}
	const maxUint = ^uint(0)
	n := (maxUint >> bits.LeadingZeros(uint(v))) + 1
	return int(n)
}

// ---------------------------------------------------------------------------
// 语料：geosite.dat 的全部类目
// ---------------------------------------------------------------------------

func geositePath(tb testing.TB) string {
	tb.Helper()
	if p := os.Getenv(geositeEnvVar); p != "" {
		return p
	}
	return filepath.Join("..", "..", "..", "resources", "geosite.dat")
}

// loadGeositeCategories 读 geosite.dat 的**全部**类目。
// 不只取 category-ads-all：仓内那份的 ads 类目只有 891 条，够不到生产规模；
// 全部类目合起来才有生产量级的 pattern 数，而且同一个域名出现在多个类目里，
// 天然造出 CSR 必须处理的**多值规则**。
// 读不到必须失败，不能 skip —— skip 掉的差分测试与不存在的差分测试没有区别。
func loadGeositeCategories(tb testing.TB) []*geodata.GeoSite {
	tb.Helper()
	path := geositePath(tb)
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("读不到 geosite.dat %q（差分测试失去被测语料，按失败处理）: %v", path, err)
	}
	var list geodata.GeoSiteList
	if err := proto.Unmarshal(raw, &list); err != nil {
		tb.Fatalf("解析 geosite.dat %q 失败: %v", path, err)
	}
	if len(list.Entry) == 0 {
		tb.Fatalf("geosite.dat %q 里一个类目都没有", path)
	}
	return list.Entry
}

type patternEntry struct {
	matcher strmatcher.Matcher
	value   uint32
}

// geositePatterns 把每个类目的 Full/Domain 条目转成 matcher。
//
// value 取**类目下标**，与 geodata.BuildMatcher 一致：一条 DomainRule_Geosite 展开成
// 该类目的全部域名，它们共享同一个 rule index。于是同一个域名出现在 N 个类目里就得到
// N 个值 —— 这正是 CSR 的多值分支，用真实数据覆盖，不靠我手工编造的样本。
func geositePatterns(tb testing.TB) []patternEntry {
	tb.Helper()
	cats := loadGeositeCategories(tb)

	out := make([]patternEntry, 0, 1<<17)
	uniq := make(map[string]struct{}, 1<<16)
	for catIdx, cat := range cats {
		for _, d := range cat.Domain {
			var (
				m   strmatcher.Matcher
				err error
			)
			switch d.Type {
			case geodata.Domain_Full:
				m, err = strmatcher.Full.New(strings.ToLower(d.Value))
			case geodata.Domain_Domain:
				m, err = strmatcher.Domain.New(strings.ToLower(d.Value))
			default:
				continue // Substr / Regex 不进 mph 组
			}
			if err != nil {
				continue
			}
			uniq[strings.ToLower(d.Value)] = struct{}{}
			out = append(out, patternEntry{matcher: m, value: uint32(catIdx)})
		}
	}

	if len(uniq) < uniquePatternFloor {
		tb.Fatalf("只解析出 %d 个唯一 pattern，低于扫描地板 %d —— 语料塌了，比对全过没有意义",
			len(uniq), uniquePatternFloor)
	}
	tb.Logf("语料：%d 个类目 / %d 条 Full|Domain 条目 / %d 个唯一 pattern", len(cats), len(out), len(uniq))
	return out
}

func feedLegacy(entries []patternEntry) *legacyMphMatcherGroup {
	g := newLegacyMphMatcherGroup()
	for _, e := range entries {
		switch m := e.matcher.(type) {
		case strmatcher.FullMatcher:
			g.AddFullMatcher(m, e.value)
		case strmatcher.DomainMatcher:
			g.AddDomainMatcher(m, e.value)
		}
	}
	if err := g.Build(); err != nil {
		panic(err)
	}
	return g
}

func feedCompact(entries []patternEntry) *strmatcher.MphMatcherGroup {
	g := strmatcher.NewMphMatcherGroup()
	for _, e := range entries {
		switch m := e.matcher.(type) {
		case strmatcher.FullMatcher:
			g.AddFullMatcher(m, e.value)
		case strmatcher.DomainMatcher:
			g.AddDomainMatcher(m, e.value)
		}
	}
	if err := g.Build(); err != nil {
		panic(err)
	}
	return g
}

// probeCorpus 造查询语料：命中面（精确、子域、多级子域）与未命中面（改写首字母、
// 纯噪声）都要有。只喂命中样本的话，一个恒真的 MatchAny 也能全绿。
func probeCorpus(entries []patternEntry) []string {
	corpus := make([]string, 0, len(entries)*3)
	for i, e := range entries {
		p := strings.ToLower(e.matcher.Pattern())
		corpus = append(corpus, p, "www."+p)
		if i%3 == 0 {
			corpus = append(corpus, "a.b."+p)
		}
		if i%5 == 0 && len(p) > 1 {
			corpus = append(corpus, "z"+p[1:]) // 首字母改写 —— 多数应当不命中
		}
	}
	corpus = append(corpus,
		"", ".", "..", "com", ".com", "example.com.", "no-such-host-xyzzy.invalid",
		"a.no-such-host-xyzzy.invalid", "EXAMPLE.COM",
	)
	return corpus
}

// ---------------------------------------------------------------------------
// 差分测试
// ---------------------------------------------------------------------------

func TestMphCompactDecidesIdenticallyToLegacyOnProductionGeosite(t *testing.T) {
	entries := geositePatterns(t)
	legacy := feedLegacy(entries)
	compact := feedCompact(entries)
	corpus := probeCorpus(entries)

	if n := compact.RuleCount(); n < mphRuleFloor {
		t.Fatalf("MPH 只建了 %d 条规则，低于地板 %d —— 表没建到量级，逐条比对的说服力不成立",
			n, mphRuleFloor)
	}
	t.Logf("语料：%d 条 pattern，%d 条 MPH 规则，%d 次查询", len(entries), compact.RuleCount(), len(corpus))

	var hits int
	for _, input := range corpus {
		gotLegacy := legacy.Match(input)
		gotCompact := compact.Match(input)
		if !reflect.DeepEqual(gotLegacy, gotCompact) {
			t.Fatalf("Match(%q) 判定不一致：legacy=%v compact=%v", input, gotLegacy, gotCompact)
		}
		if anyLegacy, anyCompact := legacy.MatchAny(input), compact.MatchAny(input); anyLegacy != anyCompact {
			t.Fatalf("MatchAny(%q) 判定不一致：legacy=%v compact=%v", input, anyLegacy, anyCompact)
		}
		if len(gotLegacy) > 0 {
			hits++
		}
	}

	// 阴性观测先证明到达：如果一次都没命中，上面每一条 DeepEqual 都在比较两个 nil，
	// 「完全一致」于是不证明任何东西。
	if hits < matchHitFloor {
		t.Fatalf("整个语料只命中 %d 次（地板 %d）—— 比对没有到达被测逻辑", hits, matchHitFloor)
	}
	t.Logf("命中 %d 次 / 共 %d 次查询", hits, len(corpus))
}

// multiValuePatterns 从语料里挑出**真实**的多值 pattern：同一个域名出现在多个
// geosite 类目里，就会带着多个 value 落到同一条 MPH 规则上。
// 不用手工编造的样本 —— 编造的样本证明不了生产数据里那条路径被走到过。
func multiValuePatterns(tb testing.TB, entries []patternEntry, want int) []string {
	tb.Helper()
	seen := make(map[string]map[uint32]struct{}, len(entries))
	for _, e := range entries {
		p := strings.ToLower(e.matcher.Pattern())
		if seen[p] == nil {
			seen[p] = make(map[uint32]struct{}, 2)
		}
		seen[p][e.value] = struct{}{}
	}
	out := make([]string, 0, want)
	for p, vals := range seen {
		if len(vals) > 1 {
			out = append(out, p)
			if len(out) == want {
				break
			}
		}
	}
	if len(out) == 0 {
		tb.Fatal("语料里一条多值 pattern 都没有 —— CSR 的多值分支无法被覆盖，这条测试会是空转的")
	}
	sort.Strings(out)
	return out
}

func TestMphCompactPreservesMultiValueRules(t *testing.T) {
	// 多值分支单独钉一次：CSR 把「每条规则的值」从独立 slice 改成大数组上的窗口，
	// 窗口边界算错时单值规则往往还是对的，错的恰好是多值那一条。
	entries := geositePatterns(t)
	legacy := feedLegacy(entries)
	compact := feedCompact(entries)

	patterns := multiValuePatterns(t, entries, 200)
	t.Logf("真实多值 pattern %d 条，样例 %q", len(patterns), patterns[0])

	var multi int
	for _, p := range patterns {
		for _, input := range []string{p, "www." + p} {
			gotLegacy := legacy.Match(input)
			gotCompact := compact.Match(input)
			if !reflect.DeepEqual(gotLegacy, gotCompact) {
				t.Fatalf("Match(%q) 多值判定不一致：legacy=%v compact=%v", input, gotLegacy, gotCompact)
			}
			if len(gotCompact) > 1 {
				multi++
			}
		}
	}
	if multi == 0 {
		t.Fatal("没有任何一条查询返回多个值 —— 多值分支没有被覆盖，这条测试是空转的")
	}
	t.Logf("其中 %d 次查询返回了多个值", multi)
}

// ---------------------------------------------------------------------------
// 内存实测
// ---------------------------------------------------------------------------

func retainedHeap(build func() any) (uint64, any) {
	for i := 0; i < 3; i++ {
		runtime.GC()
	}
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	held := build()

	for i := 0; i < 3; i++ {
		runtime.GC()
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(held)

	if after.HeapAlloc < before.HeapAlloc {
		return 0, held
	}
	return after.HeapAlloc - before.HeapAlloc, held
}

func TestMphCompactActuallyShrinksRetainedHeap(t *testing.T) {
	// 语料在 build 内部就地释放，匹配生产路径（BuildMatcher 里 domains[j] = nil）：
	// 改动前那些 pattern 字符串只被 matcher 的 rules 持有，改动后被 blob 复制、原串成垃圾。
	// 若把语料留在外面存活，改动前的真实占用会被低估。
	legacyBytes, legacyHeld := retainedHeap(func() any {
		return feedLegacy(geositePatterns(t))
	})
	compactBytes, compactHeld := retainedHeap(func() any {
		return feedCompact(geositePatterns(t))
	})
	runtime.KeepAlive(legacyHeld)
	runtime.KeepAlive(compactHeld)

	mb := func(v uint64) string { return fmt.Sprintf("%.2f MB", float64(v)/(1<<20)) }
	t.Logf("retained heap: legacy=%s compact=%s saved=%s",
		mb(legacyBytes), mb(compactBytes), mb(legacyBytes-compactBytes))

	if legacyBytes == 0 {
		t.Fatal("legacy 实现测出的常驻堆是 0 —— 测量本身坏了，任何比值都无意义")
	}
	ratio := float64(compactBytes) / float64(legacyBytes)
	t.Logf("compact/legacy = %.3f（天花板 %.2f）", ratio, compactionRatioCeiling)
	if ratio > compactionRatioCeiling {
		t.Errorf("紧凑后仍占 legacy 的 %.1f%%，超过天花板 %.0f%% —— 每条规则的 slice header 没有真的被拿掉",
			ratio*100, compactionRatioCeiling*100)
	}
}
