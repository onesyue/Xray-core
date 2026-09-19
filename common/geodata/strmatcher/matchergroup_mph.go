package strmatcher

import (
	"math/bits"
	"runtime"
	"sort"
	"strings"
	"unsafe"
)

// PrimeRK is the prime base used in Rabin-Karp algorithm.
const PrimeRK = 16777619

// RollingHash calculates the rolling murmurHash of given string based on a provided suffix hash.
func RollingHash(hash uint32, input string) uint32 {
	for i := len(input) - 1; i >= 0; i-- {
		hash = hash*PrimeRK + uint32(input[i])
	}
	return hash
}

// MemHash is the hash function used by go map, it utilizes available hardware instructions(behaves
// as aeshash if aes instruction is available).
// With different seed, each MemHash<seed> performs as distinct hash functions.
func MemHash(seed uint32, input string) uint32 {
	return uint32(strhash(unsafe.Pointer(&input), uintptr(seed))) // nosemgrep
}

const (
	mphMatchTypeCount = 2 // Full and Domain
)

type mphRuleInfo struct {
	rollingHash uint32
	matchers    [mphMatchTypeCount][]uint32
}

// mphBuildState holds everything that is only needed while rules are being
// added. Build() converts it into the compact runtime representation and drops
// it, so none of it stays resident for the life of the process.
type mphBuildState struct {
	rules     []string // RuleIdx -> pattern string, index 0 reserved for failed lookup
	ruleInfos map[string]mphRuleInfo
}

// MphMatcherGroup is an implementation of MatcherGroup.
// It implements Rabin-Karp algorithm and minimal perfect hash table for Full and Domain matcher.
//
// The resident representation is deliberately header-free. A fleet VLESS process
// holds one of these per geosite category it routes on, and `geosite:category-ads-all`
// alone expands to 189,166 domains -> 378,332 rules; at that size the per-rule
// bookkeeping dominated the table:
//
//   - `values [][]uint32` cost a 24-byte slice header plus a separate (rounded up
//     to an 8-byte size class) backing array per rule, while virtually every rule
//     carries exactly one uint32. It is now a CSR pair: `valueOffset` indexes into
//     one flat `valueData`, so a rule costs 4 bytes of index plus 4 bytes per value.
//   - `rules []string` cost a 16-byte string header per rule plus one separately
//     allocated (and separately size-class-rounded) string per pattern. It is now a
//     single `ruleBlob` with `ruleStarts`/`ruleLens` indexes. Because a Domain
//     matcher registers both "x.com" and ".x.com", and the former is exactly the
//     latter's [1:], the two share one span in the blob.
//
// Lookup semantics are unchanged: `ruleAt` and `valuesAt` reconstruct exactly the
// string and the []uint32 the previous representation returned.
type MphMatcherGroup struct {
	ruleBlob    []byte   // all rule patterns concatenated; ".x.com" and "x.com" share one span
	ruleStarts  []uint32 // RuleIdx -> offset of the pattern inside ruleBlob
	ruleLens    []uint32 // RuleIdx -> length of the pattern inside ruleBlob
	valueOffset []uint32 // RuleIdx -> start of this rule's values in valueData (len == ruleCount+1)
	valueData   []uint32 // registered matcher values, Full segment before Domain segment
	level0      []uint32 // RollingHash & Mask -> seed for Memhash
	level0Mask  uint32   // Mask restricting RollingHash to 0 ~ len(level0)
	level1      []uint32 // Memhash<seed> & Mask -> stored index for rules
	level1Mask  uint32   // Mask for restricting Memhash<seed> to 0 ~ len(level1)

	build *mphBuildState // Only used while building, released once Build() completes
}

func NewMphMatcherGroup() *MphMatcherGroup {
	return &MphMatcherGroup{
		level0:     nil,
		level0Mask: 0,
		level1:     nil,
		level1Mask: 0,
		build: &mphBuildState{
			rules:     []string{""},
			ruleInfos: map[string]mphRuleInfo{},
		},
	}
}

// AddFullMatcher implements MatcherGroupForFull.
func (g *MphMatcherGroup) AddFullMatcher(matcher FullMatcher, value uint32) {
	pattern := strings.ToLower(matcher.Pattern())
	g.addPattern(0, "", pattern, matcher.Type(), value)
}

// AddDomainMatcher implements MatcherGroupForDomain.
func (g *MphMatcherGroup) AddDomainMatcher(matcher DomainMatcher, value uint32) {
	pattern := strings.ToLower(matcher.Pattern())
	hash := g.addPattern(0, "", pattern, matcher.Type(), value) // For full domain match
	g.addPattern(hash, pattern, ".", matcher.Type(), value)     // For partial domain match
}

func (g *MphMatcherGroup) addPattern(suffixHash uint32, suffixPattern string, pattern string, matcherType Type, value uint32) uint32 {
	fullPattern := pattern + suffixPattern
	info, found := g.build.ruleInfos[fullPattern]
	if !found {
		info = mphRuleInfo{rollingHash: RollingHash(suffixHash, pattern)}
		g.build.rules = append(g.build.rules, fullPattern)
	}
	info.matchers[matcherType] = append(info.matchers[matcherType], value)
	g.build.ruleInfos[fullPattern] = info
	return info.rollingHash
}

// Build builds a minimal perfect hash table for insert rules.
// Algorithm used: Hash, displace, and compress. See http://cmph.sourceforge.net/papers/esa09.pdf
func (g *MphMatcherGroup) Build() error {
	b := g.build
	ruleCount := len(b.ruleInfos)
	g.level0 = make([]uint32, nextPow2(ruleCount/4))
	g.level0Mask = uint32(len(g.level0) - 1)
	g.level1 = make([]uint32, nextPow2(ruleCount))
	g.level1Mask = uint32(len(g.level1) - 1)

	// Create buckets based on all rule's rolling hash, and flatten the per-rule
	// values into one CSR array in the same pass.
	totalRules := len(b.rules)
	g.valueOffset = make([]uint32, totalRules+1)
	g.valueData = make([]uint32, 0, totalRules)
	buckets := make([][]uint32, len(g.level0))
	for ruleIdx := 1; ruleIdx < totalRules; ruleIdx++ { // Traverse rules starting from index 1 (0 reserved for failed lookup)
		ruleInfo := b.ruleInfos[b.rules[ruleIdx]]
		bucketIdx := ruleInfo.rollingHash & g.level0Mask
		buckets[bucketIdx] = append(buckets[bucketIdx], uint32(ruleIdx))
		g.valueOffset[ruleIdx] = uint32(len(g.valueData))
		g.valueData = append(g.valueData, ruleInfo.matchers[Full]...)
		g.valueData = append(g.valueData, ruleInfo.matchers[Domain]...)
	}
	g.valueOffset[totalRules] = uint32(len(g.valueData))
	b.ruleInfos = nil // Release the build-only index
	runtime.GC()      // peak mem

	g.packRules(b.rules)
	b.rules = nil
	g.build = nil
	runtime.GC() // peak mem

	// Sort buckets in descending order with respect to each bucket's size
	bucketIdxs := make([]int, len(buckets))
	for bucketIdx := range buckets {
		bucketIdxs[bucketIdx] = bucketIdx
	}
	sort.Slice(bucketIdxs, func(i, j int) bool { return len(buckets[bucketIdxs[i]]) > len(buckets[bucketIdxs[j]]) })

	// Exercise Hash, Displace, and Compress algorithm to construct minimal perfect hash table
	occupied := make([]bool, len(g.level1)) // Whether a second-level hash has been already used
	hashedBucket := make([]uint32, 0, 4)    // Second-level hashes for each rule in a specific bucket
	for _, bucketIdx := range bucketIdxs {
		bucket := buckets[bucketIdx]
		hashedBucket = hashedBucket[:0]
		seed := uint32(0)
		for len(hashedBucket) != len(bucket) {
			for _, ruleIdx := range bucket {
				memHash := MemHash(seed, g.ruleAt(ruleIdx)) & g.level1Mask
				if occupied[memHash] { // Collision occurred with this seed
					for _, hash := range hashedBucket { // Revert all values in this hashed bucket
						occupied[hash] = false
						g.level1[hash] = 0
					}
					hashedBucket = hashedBucket[:0]
					seed++ // Try next seed
					break
				}
				occupied[memHash] = true
				g.level1[memHash] = ruleIdx // The final value in the hash table
				hashedBucket = append(hashedBucket, memHash)
			}
		}
		g.level0[bucketIdx] = seed // Displacement value for this bucket
	}
	return nil
}

// packRules copies every rule pattern into one contiguous blob and records where
// each one lives. A Domain matcher registers both "x.com" and ".x.com"; the former
// is byte-for-byte the latter's [1:], so dotted patterns are laid down first and
// their undotted suffix is pointed at the same span instead of being copied again.
func (g *MphMatcherGroup) packRules(rules []string) {
	upperBound := 0
	for _, rule := range rules {
		upperBound += len(rule)
	}

	placed := make(map[string]uint32, len(rules))
	blob := make([]byte, 0, upperBound)

	// Pass 1: dotted patterns, so that their undotted suffix can share the span.
	for _, rule := range rules {
		if len(rule) == 0 || rule[0] != '.' {
			continue
		}
		if _, ok := placed[rule]; ok {
			continue
		}
		offset := uint32(len(blob))
		blob = append(blob, rule...)
		placed[rule] = offset
		if _, ok := placed[rule[1:]]; !ok {
			placed[rule[1:]] = offset + 1
		}
	}
	// Pass 2: everything not already covered by a dotted pattern.
	for _, rule := range rules {
		if len(rule) == 0 {
			continue
		}
		if _, ok := placed[rule]; ok {
			continue
		}
		offset := uint32(len(blob))
		blob = append(blob, rule...)
		placed[rule] = offset
	}

	// upperBound assumed no sharing, so the blob is over-allocated by exactly the
	// bytes sharing saved. Hand back that slack instead of keeping it resident.
	g.ruleBlob = make([]byte, len(blob))
	copy(g.ruleBlob, blob)

	g.ruleStarts = make([]uint32, len(rules))
	g.ruleLens = make([]uint32, len(rules))
	for ruleIdx, rule := range rules {
		g.ruleLens[ruleIdx] = uint32(len(rule))
		if len(rule) != 0 {
			g.ruleStarts[ruleIdx] = placed[rule]
		}
	}
}

// ruleAt returns the pattern registered under a rule index. The returned string
// aliases ruleBlob, which is never mutated after Build, and is only ever used for
// comparison inside this package.
func (g *MphMatcherGroup) ruleAt(ruleIdx uint32) string {
	length := g.ruleLens[ruleIdx]
	if length == 0 {
		return ""
	}
	return unsafe.String(&g.ruleBlob[g.ruleStarts[ruleIdx]], int(length))
}

// valuesAt returns the matcher values registered under a rule index, Full matcher
// values first. The window is capacity-capped so a caller appending to the result
// cannot reach into the next rule's values.
func (g *MphMatcherGroup) valuesAt(ruleIdx uint32) []uint32 {
	lo, hi := g.valueOffset[ruleIdx], g.valueOffset[ruleIdx+1]
	if lo == hi {
		return nil
	}
	return g.valueData[lo:hi:hi]
}

// RuleCount returns the number of rules held by the table, excluding the
// reserved index 0. It exists so a guard can assert the table was actually
// built at scale: "0 rules" and "every rule agrees" otherwise look alike.
func (g *MphMatcherGroup) RuleCount() int {
	if g.build != nil {
		return len(g.build.rules) - 1
	}
	if len(g.ruleLens) == 0 {
		return 0
	}
	return len(g.ruleLens) - 1
}

// Lookup searches for input in minimal perfect hash table and returns its index. 0 indicates not found.
func (g *MphMatcherGroup) Lookup(rollingHash uint32, input string) uint32 {
	i0 := rollingHash & g.level0Mask
	seed := g.level0[i0]
	i1 := MemHash(seed, input) & g.level1Mask
	if n := g.level1[i1]; g.ruleAt(n) == input {
		return n
	}
	return 0
}

// Match implements MatcherGroup.Match.
func (g *MphMatcherGroup) Match(input string) []uint32 {
	matches := make([][]uint32, 0, 5)
	hash := uint32(0)
	for i := len(input) - 1; i >= 0; i-- {
		hash = hash*PrimeRK + uint32(input[i])
		if input[i] == '.' {
			if mphIdx := g.Lookup(hash, input[i:]); mphIdx != 0 {
				matches = append(matches, g.valuesAt(mphIdx))
			}
		}
	}
	if mphIdx := g.Lookup(hash, input); mphIdx != 0 {
		matches = append(matches, g.valuesAt(mphIdx))
	}
	return CompositeMatchesReverse(matches)
}

// MatchAny implements MatcherGroup.MatchAny.
func (g *MphMatcherGroup) MatchAny(input string) bool {
	hash := uint32(0)
	for i := len(input) - 1; i >= 0; i-- {
		hash = hash*PrimeRK + uint32(input[i])
		if input[i] == '.' {
			if g.Lookup(hash, input[i:]) != 0 {
				return true
			}
		}
	}
	return g.Lookup(hash, input) != 0
}

func nextPow2(v int) int {
	if v <= 1 {
		return 1
	}
	const MaxUInt = ^uint(0)
	n := (MaxUInt >> bits.LeadingZeros(uint(v))) + 1
	return int(n)
}

//go:noescape
//go:linkname strhash runtime.strhash
func strhash(p unsafe.Pointer, h uintptr) uintptr
