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
	"sort"
	"testing"

	auth "istio.io/api/security/v1beta1"
	securityclient "istio.io/client-go/pkg/apis/security/v1"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pkg/config/schema/gvk"
	"istio.io/istio/pkg/config/schema/kind"
	"istio.io/istio/pkg/kube/controllers"
	"istio.io/istio/pkg/kube/krt"
	"istio.io/istio/pkg/test/util/assert"
	"istio.io/istio/pkg/util/sets"
	workloadsecurity "istio.io/istio/pkg/workloadapi/security"
)

func TestPoliciesRequestedAndFull(t *testing.T) {
	s := newAmbientTestServer(t, testC, testNW, "")

	s.addPolicy(t, "authorization", testNS, nil, gvk.AuthorizationPolicy, nil)
	s.assertEvent(t, "authorization")

	const peerAuthenticationName = "peer-authentication"
	convertedPeerAuthenticationName := model.GetAmbientPolicyConfigName(model.ConfigKey{
		Kind:      kind.PeerAuthentication,
		Name:      peerAuthenticationName,
		Namespace: testNS,
	})
	s.addPolicy(t, peerAuthenticationName, testNS, map[string]string{"app": "a"}, gvk.PeerAuthentication, func(o controllers.Object) {
		policy := o.(*securityclient.PeerAuthentication)
		policy.Spec.Mtls = &auth.PeerAuthentication_MutualTLS{
			Mode: auth.PeerAuthentication_MutualTLS_PERMISSIVE,
		}
		policy.Spec.PortLevelMtls = map[uint32]*auth.PeerAuthentication_MutualTLS{
			9090: {
				Mode: auth.PeerAuthentication_MutualTLS_STRICT,
			},
		}
	})
	s.assertEvent(t, convertedPeerAuthenticationName, staticStrictPolicyName)

	requested := sets.New(
		model.ConfigKey{Kind: kind.AuthorizationPolicy, Name: "authorization", Namespace: testNS},
		model.ConfigKey{Kind: kind.AuthorizationPolicy, Name: convertedPeerAuthenticationName, Namespace: testNS},
		model.ConfigKey{Kind: kind.AuthorizationPolicy, Name: "deleted", Namespace: testNS},
	)
	assert.Equal(t, policyResourceNames(s.Policies(requested)), []string{
		testNS + "/authorization",
		testNS + "/" + convertedPeerAuthenticationName,
	})
	assert.Equal(t, policyResourceNames(s.Policies(nil)), []string{
		systemNS + "/" + staticStrictPolicyName,
		testNS + "/authorization",
		testNS + "/" + convertedPeerAuthenticationName,
	})
}

func policyResourceNames(policies []model.WorkloadAuthorization) []string {
	result := make([]string, 0, len(policies))
	for _, policy := range policies {
		if policy.Authorization != nil {
			result = append(result, policy.ResourceName())
		}
	}
	sort.Strings(result)
	return result
}

const (
	benchmarkPolicyCount   = 10_000
	benchmarkRequestCount  = 16
	benchmarkPolicyNS      = "benchmark"
	benchmarkPolicyNameFmt = "policy-%05d"
)

var benchmarkPoliciesResult []model.WorkloadAuthorization

func BenchmarkPoliciesIncremental10000PoliciesOneUpdated(b *testing.B) {
	policies := make([]model.WorkloadAuthorization, benchmarkPolicyCount)
	requests := make([]sets.Set[model.ConfigKey], benchmarkRequestCount)
	expectedNames := make([]string, benchmarkRequestCount)
	for i := range policies {
		policies[i] = model.WorkloadAuthorization{
			Authorization: &workloadsecurity.Authorization{
				Name:      fmt.Sprintf(benchmarkPolicyNameFmt, i),
				Namespace: benchmarkPolicyNS,
			},
		}
	}
	for i := range requests {
		name := policies[i*(benchmarkPolicyCount/benchmarkRequestCount)].Authorization.Name
		requests[i] = sets.New(model.ConfigKey{
			Kind:      kind.AuthorizationPolicy,
			Name:      name,
			Namespace: benchmarkPolicyNS,
		})
		expectedNames[i] = name
	}

	idx := &index{authorizationPolicies: krt.NewStaticCollection(nil, policies)}
	for i, request := range requests {
		result := idx.Policies(request)
		if len(result) != 1 || result[0].Authorization.GetName() != expectedNames[i] {
			b.Fatalf("Policies(%v) = %v, want %q", request, policyResourceNames(result), expectedNames[i])
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkPoliciesResult = idx.Policies(requests[i&(benchmarkRequestCount-1)])
	}
}
