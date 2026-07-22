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

package xds

import (
	"context"
	"testing"

	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"

	"istio.io/istio/pilot/pkg/model"
	v3 "istio.io/istio/pilot/pkg/xds/v3"
	istiolog "istio.io/istio/pkg/log"
)

const resourceSizeBenchmarkPayloadBytes = 128

type resourceSizeBenchmarkGenerator struct {
	resources model.Resources
}

func (g resourceSizeBenchmarkGenerator) Generate(*model.Proxy, *model.WatchedResource, *model.PushRequest) (model.Resources, model.XdsLogDetails, error) {
	return g.resources, model.DefaultXdsLogDetails, nil
}

type resourceSizeBenchmarkStream struct {
	grpc.ServerStream
	response *discovery.DiscoveryResponse
}

func (s *resourceSizeBenchmarkStream) Send(response *discovery.DiscoveryResponse) error {
	s.response = response
	return nil
}

func (*resourceSizeBenchmarkStream) Recv() (*discovery.DiscoveryRequest, error) {
	return nil, nil
}

func (*resourceSizeBenchmarkStream) Context() context.Context {
	return context.Background()
}

// newResourceSizePushOperation constructs the production pushXds default-log path
// with a generated resource set. Its signature intentionally keeps xDS server and
// connection internals local to this in-package benchmark fixture.
func newResourceSizePushOperation(t testing.TB, resourceCount int) func() error {
	t.Helper()

	payload := make([]byte, resourceSizeBenchmarkPayloadBytes)
	resources := make(model.Resources, resourceCount)
	for i := range resources {
		resources[i] = &discovery.Resource{Resource: &anypb.Any{
			TypeUrl: "type.googleapis.com/benchmark.Resource",
			Value:   payload,
		}}
	}

	stream := &resourceSizeBenchmarkStream{}
	server := &DiscoveryServer{Generators: map[string]model.XdsResourceGenerator{
		v3.ClusterType: resourceSizeBenchmarkGenerator{resources: resources},
	}}
	connection := newConnection("benchmark", stream)
	connection.s = server
	connection.proxy = &model.Proxy{
		ID:               "benchmark",
		Metadata:         &model.NodeMetadata{},
		WatchedResources: make(map[string]*model.WatchedResource),
	}
	watched := &model.WatchedResource{TypeUrl: v3.ClusterType}
	request := &model.PushRequest{Push: &model.PushContext{PushVersion: "benchmark"}}

	oldLogLevel := log.GetOutputLevel()
	log.SetOutputLevel(istiolog.NoneLevel)
	t.Cleanup(func() {
		log.SetOutputLevel(oldLogLevel)
	})

	push := func() error {
		return server.pushXds(connection, watched, request)
	}
	if err := push(); err != nil {
		t.Fatalf("pushXds failed: %v", err)
	}
	if stream.response == nil {
		t.Fatal("pushXds did not send a response")
	}
	if got := len(stream.response.Resources); got != resourceCount {
		t.Fatalf("sent %d resources, want %d", got, resourceCount)
	}
	if got, want := ResourceSize(resources), resourceCount*resourceSizeBenchmarkPayloadBytes; got != want {
		t.Fatalf("ResourceSize() = %d, want %d", got, want)
	}
	return push
}

func TestPushXdsResourceSize(t *testing.T) {
	push := newResourceSizePushOperation(t, 3)
	if err := push(); err != nil {
		t.Fatalf("pushXds failed: %v", err)
	}
}

func BenchmarkPushXdsResourceSize(b *testing.B) {
	push := newResourceSizePushOperation(b, 4096)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := push(); err != nil {
			b.Fatal(err)
		}
	}
}
