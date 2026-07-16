// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ambient

import (
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	auth "istio.io/api/security/v1beta1"
	typev1beta1 "istio.io/api/type/v1beta1"
	securityclient "istio.io/client-go/pkg/apis/security/v1"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pilot/pkg/serviceregistry/util/xdsfake"
	"istio.io/istio/pkg/config/mesh/meshwatcher"
	"istio.io/istio/pkg/config/schema/kind"
	"istio.io/istio/pkg/kube/krt"
	istiolog "istio.io/istio/pkg/log"
	"istio.io/istio/pkg/test"
	"istio.io/istio/pkg/util/sets"
)

const (
	joinedBenchmarkPolicyNS      = "benchmark"
	joinedBenchmarkPolicyNameFmt = "policy-%05d"
	joinedPeerAuthenticationName = "peer-authentication"
	joinedWaypointName           = "waypoint"
)

var benchmarkJoinedPoliciesResult []model.WorkloadAuthorization

type joinedPolicyLookup uint8

const (
	joinedAuthorizationPolicyLookup joinedPolicyLookup = iota
	joinedPeerAuthenticationLookup
	joinedDefaultPolicyLookup
	joinedWaypointPolicyLookup
	joinedMissingPolicyLookup
)

type joinedPolicyBenchmarkCase struct {
	policyCount int
	lookup      joinedPolicyLookup
}

func BenchmarkPoliciesJoined0PoliciesOneUpdated(b *testing.B) {
	benchmarkPoliciesJoinedOneUpdated(b, joinedPolicyBenchmarkCase{lookup: joinedAuthorizationPolicyLookup})
}

func BenchmarkPoliciesJoined1PoliciesOneUpdated(b *testing.B) {
	benchmarkPoliciesJoinedOneUpdated(b, joinedPolicyBenchmarkCase{policyCount: 1, lookup: joinedAuthorizationPolicyLookup})
}

func BenchmarkPoliciesJoined16PoliciesOneUpdated(b *testing.B) {
	benchmarkPoliciesJoinedOneUpdated(b, joinedPolicyBenchmarkCase{policyCount: 16, lookup: joinedAuthorizationPolicyLookup})
}

func BenchmarkPoliciesJoined10000PoliciesOneUpdated(b *testing.B) {
	benchmarkPoliciesJoinedOneUpdated(b, joinedPolicyBenchmarkCase{policyCount: 10_000, lookup: joinedAuthorizationPolicyLookup})
}

func BenchmarkPoliciesJoined10000PoliciesPeerAuthenticationUpdated(b *testing.B) {
	benchmarkPoliciesJoinedOneUpdated(b, joinedPolicyBenchmarkCase{policyCount: 10_000, lookup: joinedPeerAuthenticationLookup})
}

func BenchmarkPoliciesJoined10000PoliciesDefaultUpdated(b *testing.B) {
	benchmarkPoliciesJoinedOneUpdated(b, joinedPolicyBenchmarkCase{policyCount: 10_000, lookup: joinedDefaultPolicyLookup})
}

func BenchmarkPoliciesJoined10000PoliciesWaypointUpdated(b *testing.B) {
	benchmarkPoliciesJoinedOneUpdated(b, joinedPolicyBenchmarkCase{policyCount: 10_000, lookup: joinedWaypointPolicyLookup})
}

func BenchmarkPoliciesJoined10000PoliciesMissingUpdated(b *testing.B) {
	benchmarkPoliciesJoinedOneUpdated(b, joinedPolicyBenchmarkCase{policyCount: 10_000, lookup: joinedMissingPolicyLookup})
}

func benchmarkPoliciesJoinedOneUpdated(b *testing.B, tc joinedPolicyBenchmarkCase) {
	configureJoinedPoliciesBenchmark(b)
	stop := test.NewStop(b)
	opts := krt.NewOptionsBuilder(stop, "benchmark", nil)
	policies := make([]*securityclient.AuthorizationPolicy, tc.policyCount)
	for i := range policies {
		policies[i] = &securityclient.AuthorizationPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf(joinedBenchmarkPolicyNameFmt, i),
				Namespace: joinedBenchmarkPolicyNS,
			},
			Spec: auth.AuthorizationPolicy{
				Action: auth.AuthorizationPolicy_ALLOW,
			},
		}
	}
	includePeerAuthentication := tc.lookup == joinedPeerAuthenticationLookup || tc.lookup == joinedDefaultPolicyLookup || tc.lookup == joinedMissingPolicyLookup
	includeWaypoint := tc.lookup == joinedWaypointPolicyLookup || tc.lookup == joinedMissingPolicyLookup
	peerAuthentications := make([]*securityclient.PeerAuthentication, 0, 1)
	if includePeerAuthentication {
		peerAuthentications = append(peerAuthentications, joinedPeerAuthentication())
	}
	waypointPolicies := make([]Waypoint, 0, 1)
	flags := FeatureFlags{}
	if includeWaypoint {
		flags.DefaultAllowFromWaypoint = true
		waypointPolicies = append(waypointPolicies, Waypoint{
			Named:           krt.Named{Name: joinedWaypointName, Namespace: joinedBenchmarkPolicyNS},
			ServiceAccounts: []string{"waypoint"},
		})
	}

	authorizationPolicies := krt.NewStaticCollection(nil, policies, opts.WithName("AuthorizationPolicies")...)
	peers := krt.NewStaticCollection(nil, peerAuthentications, opts.WithName("PeerAuthentications")...)
	waypoints := krt.NewStaticCollection(nil, waypointPolicies, opts.WithName("Waypoints")...)
	meshConfig := meshwatcher.NewTestWatcher(nil)
	idx := &index{
		meshConfig: meshConfig,
		XDSUpdater: xdsfake.NewFakeXDS(),
		Flags:      flags,
	}
	_, allPolicies := idx.buildAndRegisterPolicyCollections(authorizationPolicies, peers, waypoints, opts)
	idx.authorizationPolicies = allPolicies
	if !allPolicies.WaitUntilSynced(stop) {
		b.Fatal("AllPolicies did not sync")
	}
	wantPolicyCount := tc.policyCount
	if includePeerAuthentication {
		wantPolicyCount += 2 // The converted PeerAuthentication and static strict policy.
	}
	if includeWaypoint {
		wantPolicyCount++
	}
	if got := len(allPolicies.List()); got != wantPolicyCount {
		b.Fatalf("AllPolicies has %d policies, want %d", got, wantPolicyCount)
	}

	name, namespace, found := joinedPolicyLookupTarget(tc, meshConfig, flags)
	request := sets.New(model.ConfigKey{
		Kind:      kind.AuthorizationPolicy,
		Name:      name,
		Namespace: namespace,
	})
	result := idx.Policies(request)
	if !found {
		if len(result) != 0 {
			b.Fatalf("Policies(%v) returned %d policies, want 0", request, len(result))
		}
	} else if len(result) != 1 || result[0].Authorization == nil || result[0].ResourceName() != namespace+"/"+name {
		b.Fatalf("Policies(%v) = %v, want %s/%s", request, policyResourceNames(result), namespace, name)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkJoinedPoliciesResult = idx.Policies(request)
	}
}

func joinedPolicyLookupTarget(tc joinedPolicyBenchmarkCase, meshConfig meshwatcher.TestWatcher, flags FeatureFlags) (string, string, bool) {
	switch tc.lookup {
	case joinedAuthorizationPolicyLookup:
		if tc.policyCount == 0 {
			return "missing", joinedBenchmarkPolicyNS, false
		}
		return fmt.Sprintf(joinedBenchmarkPolicyNameFmt, tc.policyCount/2), joinedBenchmarkPolicyNS, true
	case joinedPeerAuthenticationLookup:
		return model.GetAmbientPolicyConfigName(model.ConfigKey{
			Kind:      kind.PeerAuthentication,
			Name:      joinedPeerAuthenticationName,
			Namespace: joinedBenchmarkPolicyNS,
		}), joinedBenchmarkPolicyNS, true
	case joinedDefaultPolicyLookup:
		return staticStrictPolicyName, meshConfig.Mesh().GetRootNamespace(), true
	case joinedWaypointPolicyLookup:
		waypoint := Waypoint{Named: krt.Named{Name: joinedWaypointName, Namespace: joinedBenchmarkPolicyNS}, ServiceAccounts: []string{"waypoint"}}
		return implicitWaypointPolicyName(flags, &waypoint), joinedBenchmarkPolicyNS, true
	case joinedMissingPolicyLookup:
		return "missing", joinedBenchmarkPolicyNS, false
	default:
		panic(fmt.Sprintf("unknown joined policy lookup %d", tc.lookup))
	}
}

func joinedPeerAuthentication() *securityclient.PeerAuthentication {
	return &securityclient.PeerAuthentication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      joinedPeerAuthenticationName,
			Namespace: joinedBenchmarkPolicyNS,
		},
		Spec: auth.PeerAuthentication{
			Selector: &typev1beta1.WorkloadSelector{MatchLabels: map[string]string{"app": "a"}},
			Mtls: &auth.PeerAuthentication_MutualTLS{
				Mode: auth.PeerAuthentication_MutualTLS_PERMISSIVE,
			},
			PortLevelMtls: map[uint32]*auth.PeerAuthentication_MutualTLS{
				9090: {
					Mode: auth.PeerAuthentication_MutualTLS_STRICT,
				},
			},
		},
	}
}

func configureJoinedPoliciesBenchmark(b *testing.B) {
	for _, scope := range istiolog.Scopes() {
		scope := scope
		level := scope.GetOutputLevel()
		scope.SetOutputLevel(istiolog.NoneLevel)
		b.Cleanup(func() {
			scope.SetOutputLevel(level)
		})
	}
}
