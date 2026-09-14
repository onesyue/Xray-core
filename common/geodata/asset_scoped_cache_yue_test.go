package geodata

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/xtls/xray-core/common/geodata/strmatcher"
	"github.com/xtls/xray-core/common/platform"
	"github.com/xtls/xray-core/common/utils"
	"google.golang.org/protobuf/proto"
)

// The matcher caches in this package are process-global while
// xray.location.asset is per-instance. An embedder that runs several instances
// in one process, each with its own geodata directory, must not have the second
// instance silently served the first one's data because both reference
// "geosite.dat:CODE". These tests build the same code from two directories
// through one factory and require each matcher to see only its own directory.

type assetFixture struct {
	dir    string
	domain string
	ip     net.IP
}

func writeAssetFixtures(t *testing.T) []assetFixture {
	t.Helper()
	fixtures := []assetFixture{
		{dir: filepath.Join(t.TempDir(), "first"), domain: "first.example", ip: net.IPv4(203, 0, 113, 1)},
		{dir: filepath.Join(t.TempDir(), "second"), domain: "second.example", ip: net.IPv4(198, 51, 100, 1)},
	}
	for _, f := range fixtures {
		if err := os.MkdirAll(f.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// Same code in both directories: that collision is the whole point.
		site, err := proto.Marshal(&GeoSiteList{Entry: []*GeoSite{{
			Code:   "SHARED",
			Domain: []*Domain{{Type: Domain_Full, Value: f.domain}},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.dir, DefaultGeoSiteDat), site, 0o644); err != nil {
			t.Fatal(err)
		}
		ip, err := proto.Marshal(&GeoIPList{Entry: []*GeoIP{{
			Code: "SHARED",
			Cidr: []*CIDR{{Ip: f.ip.To4(), Prefix: 32}},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.dir, DefaultGeoIPDat), ip, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return fixtures
}

func assertDomainMatcherScoped(t *testing.T, factory DomainMatcherFactory) {
	t.Helper()
	fixtures := writeAssetFixtures(t)
	matchers := make([]DomainMatcher, len(fixtures))
	for i, f := range fixtures {
		t.Setenv(platform.AssetLocation, f.dir)
		m, err := factory.BuildMatcher([]*DomainRule{
			{Value: &DomainRule_Geosite{Geosite: &GeoSiteRule{File: DefaultGeoSiteDat, Code: "SHARED"}}},
		})
		if err != nil {
			t.Fatalf("BuildMatcher(%s): %v", f.dir, err)
		}
		matchers[i] = m
	}
	for i, f := range fixtures {
		if !matchers[i].MatchAny(f.domain) {
			t.Fatalf("matcher built from %s does not match its own %q: the cache served another directory", f.dir, f.domain)
		}
		for j, other := range fixtures {
			if j != i && matchers[i].MatchAny(other.domain) {
				t.Fatalf("matcher built from %s matched %q from %s: the cache key ignores the asset directory", f.dir, other.domain, other.dir)
			}
		}
	}
}

func TestMphDomainMatcherCacheIsScopedToAssetDirectory(t *testing.T) {
	assertDomainMatcherScoped(t, &MphDomainMatcherFactory{shared: utils.NewWeakCacheMap[string, strmatcher.MphValueMatcher]()})
}

func TestCompactDomainMatcherCacheIsScopedToAssetDirectory(t *testing.T) {
	assertDomainMatcherScoped(t, &CompactDomainMatcherFactory{shared: utils.NewWeakCacheMap[string, strmatcher.LinearAnyMatcher]()})
}

func TestIPSetCacheIsScopedToAssetDirectory(t *testing.T) {
	fixtures := writeAssetFixtures(t)
	factory := &IPSetFactory{shared: utils.NewWeakCacheMap[string, IPSet]()}
	sets := make([]*IPSet, len(fixtures))
	for i, f := range fixtures {
		t.Setenv(platform.AssetLocation, f.dir)
		s, err := factory.GetOrCreateFromGeoIPRules([]*GeoIPRule{{File: DefaultGeoIPDat, Code: "SHARED"}})
		if err != nil {
			t.Fatalf("GetOrCreateFromGeoIPRules(%s): %v", f.dir, err)
		}
		sets[i] = s
	}
	for i, f := range fixtures {
		m := &HeuristicIPMatcher{ipset: sets[i]}
		if !m.Match(f.ip) {
			t.Fatalf("IP set built from %s does not contain its own %s: the cache served another directory", f.dir, f.ip)
		}
		for j, other := range fixtures {
			if j != i && m.Match(other.ip) {
				t.Fatalf("IP set built from %s contains %s from %s: the cache key ignores the asset directory", f.dir, other.ip, other.dir)
			}
		}
	}
}

// TestRuleKeysCarryTheResolvedAssetPath is the direct form: the keys the
// factories memoise on must differ between two directories and must still be
// stable for one directory, or the cache would either collide or never hit.
func TestRuleKeysCarryTheResolvedAssetPath(t *testing.T) {
	fixtures := writeAssetFixtures(t)
	domainRules := []*DomainRule{{Value: &DomainRule_Geosite{Geosite: &GeoSiteRule{File: DefaultGeoSiteDat, Code: "SHARED"}}}}
	ipRules := []*GeoIPRule{{File: DefaultGeoIPDat, Code: "SHARED"}}
	seenDomain := map[string]string{}
	seenIP := map[string]string{}
	for _, f := range fixtures {
		t.Setenv(platform.AssetLocation, f.dir)
		dk, ik := buildDomainRulesKey(domainRules), buildGeoIPRulesKey(ipRules)
		if dk != buildDomainRulesKey(domainRules) || ik != buildGeoIPRulesKey(ipRules) {
			t.Fatalf("keys are not stable within one directory (%s)", f.dir)
		}
		if prior, dup := seenDomain[dk]; dup {
			t.Fatalf("domain key %q is shared by %s and %s", dk, prior, f.dir)
		}
		if prior, dup := seenIP[ik]; dup {
			t.Fatalf("geoip key %q is shared by %s and %s", ik, prior, f.dir)
		}
		seenDomain[dk], seenIP[ik] = f.dir, f.dir
	}
}
