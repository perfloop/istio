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
	"fmt"
	"testing"
	"time"

	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"

	"istio.io/istio/pilot/pkg/model"
	v3 "istio.io/istio/pilot/pkg/xds/v3"
	istiolog "istio.io/istio/pkg/log"
)

const resourceSizeBenchmarkPayloadBytes = 256

type resourceSizeBenchmarkGenerator struct {
	resources model.Resources
}

func (g resourceSizeBenchmarkGenerator) Generate(
	_ *model.Proxy,
	_ *model.WatchedResource,
	_ *model.PushRequest,
) (model.Resources, model.XdsLogDetails, error) {
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

func newResourceSizeBenchmarkInputs(resourceCount int) (
	*DiscoveryServer,
	*Connection,
	*model.WatchedResource,
	*model.PushRequest,
	*resourceSizeBenchmarkStream,
	int,
) {
	resources := make(model.Resources, resourceCount)
	expectedSize := 0
	for i := range resources {
		value := make([]byte, resourceSizeBenchmarkPayloadBytes)
		value[(i*31)%len(value)] = byte(i)
		resources[i] = &discovery.Resource{
			Name: fmt.Sprintf("benchmark-resource-%d", i),
			Resource: &anypb.Any{
				TypeUrl: "type.googleapis.com/benchmark.Resource",
				Value:   value,
			},
		}
		expectedSize += len(value)
	}

	stream := &resourceSizeBenchmarkStream{}
	proxy := &model.Proxy{
		ID:                   "resource-size-benchmark",
		Metadata:             &model.NodeMetadata{},
		XdsResourceGenerator: resourceSizeBenchmarkGenerator{resources: resources},
		WatchedResources:     map[string]*model.WatchedResource{},
	}
	connection := newConnection("resource-size-benchmark", stream)
	connection.proxy = proxy

	return &DiscoveryServer{}, connection, &model.WatchedResource{TypeUrl: v3.ClusterType}, &model.PushRequest{
		Push: &model.PushContext{PushVersion: "resource-size-benchmark"},
	}, stream, expectedSize
}

func disableResourceSizeBenchmarkLogs(t testing.TB) {
	t.Helper()
	level := log.GetOutputLevel()
	log.SetOutputLevel(istiolog.NoneLevel)
	t.Cleanup(func() {
		log.SetOutputLevel(level)
	})
}

func verifyResourceSizeBenchmarkResponse(t testing.TB, stream *resourceSizeBenchmarkStream, wantCount, wantSize int) {
	t.Helper()
	if stream.response == nil {
		t.Fatal("pushXds did not send a response")
	}
	if got := len(stream.response.Resources); got != wantCount {
		t.Fatalf("pushXds sent %d resources, want %d", got, wantCount)
	}
	gotSize := 0
	for _, resource := range stream.response.Resources {
		gotSize += len(resource.Value)
	}
	if gotSize != wantSize {
		t.Fatalf("pushXds sent resources totaling %d bytes, want %d", gotSize, wantSize)
	}
}

func TestPushXdsResourceSizeDefaultPush(t *testing.T) {
	server, connection, watched, request, stream, expectedSize := newResourceSizeBenchmarkInputs(4)
	disableResourceSizeBenchmarkLogs(t)

	if err := server.pushXds(connection, watched, request); err != nil {
		t.Fatalf("pushXds() failed: %v", err)
	}
	verifyResourceSizeBenchmarkResponse(t, stream, 4, expectedSize)
	if got := connection.proxy.WatchedResources[watched.TypeUrl].NonceSent; got != stream.response.Nonce {
		t.Fatalf("sent nonce %q, want %q", got, stream.response.Nonce)
	}
}

func BenchmarkPushXdsResourceSize(b *testing.B) {
	// Keep the resource cardinality runtime-derived so the compiler cannot specialize
	// ResourceSize for a fixed input. The benchmark models a large config response.
	resourceCount := 4096 + int(time.Now().UnixNano()&1)
	server, connection, watched, request, stream, expectedSize := newResourceSizeBenchmarkInputs(resourceCount)
	disableResourceSizeBenchmarkLogs(b)

	b.ResetTimer()
	for b.Loop() {
		if err := server.pushXds(connection, watched, request); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()

	verifyResourceSizeBenchmarkResponse(b, stream, resourceCount, expectedSize)
}
