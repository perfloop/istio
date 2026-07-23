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

var resourceSizeBenchmarkResourceCount = 16384

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

func newResourceSizeBenchmarkResources(resourceCount int) model.Resources {
	resources := make(model.Resources, resourceCount)
	for i := range resources {
		payload := make([]byte, resourceSizeBenchmarkPayloadBytes)
		for j := range payload {
			payload[j] = byte(i + j)
		}
		resources[i] = &discovery.Resource{Resource: &anypb.Any{
			TypeUrl: "type.googleapis.com/benchmark.Resource",
			Value:   payload,
		}}
	}
	return resources
}

func TestPushXdsResourceSize(t *testing.T) {
	resources := newResourceSizeBenchmarkResources(3)
	stream := &resourceSizeBenchmarkStream{}
	server := &DiscoveryServer{Generators: map[string]model.XdsResourceGenerator{
		v3.ClusterType: resourceSizeBenchmarkGenerator{resources: resources},
	}}
	connection := newConnection("resource-size-test", stream)
	connection.proxy = &model.Proxy{
		ID:               "resource-size-test",
		Metadata:         &model.NodeMetadata{},
		WatchedResources: make(map[string]*model.WatchedResource),
	}
	watched := &model.WatchedResource{TypeUrl: v3.ClusterType}
	// An empty ConfigsUpdated set selects pushXds's default info-log branch.
	request := &model.PushRequest{Push: &model.PushContext{PushVersion: "resource-size-test"}}

	oldLogLevel := log.GetOutputLevel()
	log.SetOutputLevel(istiolog.NoneLevel)
	t.Cleanup(func() {
		log.SetOutputLevel(oldLogLevel)
	})

	if err := server.pushXds(connection, watched, request); err != nil {
		t.Fatalf("pushXds failed: %v", err)
	}
	if stream.response == nil {
		t.Fatal("pushXds did not send a response")
	}
	if got := len(stream.response.Resources); got != len(resources) {
		t.Fatalf("sent %d resources, want %d", got, len(resources))
	}
	if got, want := ResourceSize(resources), len(resources)*resourceSizeBenchmarkPayloadBytes; got != want {
		t.Fatalf("ResourceSize() = %d, want %d", got, want)
	}
}

func BenchmarkPushXdsResourceSize(b *testing.B) {
	resourceCount := resourceSizeBenchmarkResourceCount
	resources := newResourceSizeBenchmarkResources(resourceCount)
	stream := &resourceSizeBenchmarkStream{}
	server := &DiscoveryServer{Generators: map[string]model.XdsResourceGenerator{
		v3.ClusterType: resourceSizeBenchmarkGenerator{resources: resources},
	}}
	connection := newConnection("resource-size-benchmark", stream)
	connection.proxy = &model.Proxy{
		ID:               "resource-size-benchmark",
		Metadata:         &model.NodeMetadata{},
		WatchedResources: make(map[string]*model.WatchedResource),
	}
	watched := &model.WatchedResource{TypeUrl: v3.ClusterType}
	// An empty ConfigsUpdated set selects pushXds's default info-log branch.
	request := &model.PushRequest{Push: &model.PushContext{PushVersion: "resource-size-benchmark"}}

	oldLogLevel := log.GetOutputLevel()
	log.SetOutputLevel(istiolog.NoneLevel)
	b.Cleanup(func() {
		log.SetOutputLevel(oldLogLevel)
	})

	if err := server.pushXds(connection, watched, request); err != nil {
		b.Fatalf("pushXds failed: %v", err)
	}
	if stream.response == nil || len(stream.response.Resources) != resourceCount {
		b.Fatal("pushXds did not send the expected response")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := server.pushXds(connection, watched, request); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if stream.response == nil || len(stream.response.Resources) != resourceCount {
		b.Fatal("pushXds did not retain the expected response")
	}
}
