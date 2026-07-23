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

package core

import (
	"testing"

	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"

	"istio.io/istio/pilot/pkg/model"
)

func BenchmarkFinalizeOutboundListeners(b *testing.B) {
	const listenerCount = 103

	cg := NewConfigGenTest(b, TestOptions{})
	lb := NewListenerBuilder(cg.SetupProxy(getProxy()), cg.PushContext())
	listenerMap := make(map[listenerKey]*outboundListenerEntry, listenerCount)
	for port := 10000; port < 10000+listenerCount; port++ {
		listenerMap[listenerKey{bind: "10.0.0.1", port: port}] = &outboundListenerEntry{
			servicePort: &model.Port{Port: port},
			bind: listenerBinding{
				binds:      []string{"10.0.0.1"},
				bindToPort: true,
			},
		}
	}

	var listeners []*listener.Listener
	for b.Loop() {
		listeners = finalizeOutboundListeners(lb, listenerMap)
	}

	if len(listeners) != listenerCount {
		b.Fatalf("got %d listeners, want %d", len(listeners), listenerCount)
	}
	for _, listener := range listeners {
		if listener.DefaultFilterChain == nil || len(listener.DefaultFilterChain.Filters) == 0 {
			b.Fatalf("listener %q has no fallthrough filter chain", listener.Name)
		}
	}
	b.ReportMetric(float64(listenerCount), "listeners/op")
}
