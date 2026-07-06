package xds

import (
	"context"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"google.golang.org/grpc"
	istiolog "istio.io/istio/pkg/log"
	"istio.io/istio/pkg/model"
)

type mockStream struct {
	grpc.ServerStream
	recvChan chan *discovery.DiscoveryRequest
	sendChan chan *discovery.DiscoveryResponse
	ctx      context.Context
}

func (m *mockStream) Recv() (*discovery.DiscoveryRequest, error) {
	select {
	case req, ok := <-m.recvChan:
		if !ok {
			return nil, io.EOF
		}
		return req, nil
	case <-m.ctx.Done():
		return nil, m.ctx.Err()
	}
}

func (m *mockStream) Send(resp *discovery.DiscoveryResponse) error {
	select {
	case m.sendChan <- resp:
		return nil
	case <-m.ctx.Done():
		return m.ctx.Err()
	}
}

func (m *mockStream) Context() context.Context {
	return m.ctx
}

type mockConnectionContext struct {
	con            *Connection
	processDelay   time.Duration
	pushDelay      time.Duration
	processedCount int64
	pushCount      int64
	latencies      []time.Duration
	latencyMu      sync.Mutex
}

func (m *mockConnectionContext) XdsConnection() *Connection {
	return m.con
}

func (m *mockConnectionContext) Watcher() Watcher {
	return &TestProxy{}
}

func (m *mockConnectionContext) Initialize(node *core.Node) error {
	m.con.MarkInitialized()
	return nil
}

func (m *mockConnectionContext) Close() {}

func (m *mockConnectionContext) Process(req *discovery.DiscoveryRequest) error {
	if m.processDelay > 0 {
		time.Sleep(m.processDelay)
	}
	if req.VersionInfo != "" {
		if t, err := strconv.ParseInt(req.VersionInfo, 10, 64); err == nil {
			latency := time.Since(time.Unix(0, t))
			m.latencyMu.Lock()
			m.latencies = append(m.latencies, latency)
			m.latencyMu.Unlock()
		}
	}
	atomic.AddInt64(&m.processedCount, 1)
	return nil
}

func (m *mockConnectionContext) Push(ev any) error {
	if m.pushDelay > 0 {
		time.Sleep(m.pushDelay)
	}
	atomic.AddInt64(&m.pushCount, 1)
	return nil
}

func BenchmarkStreamHOL(b *testing.B) {
	opts := istiolog.DefaultOptions()
	opts.OutputPaths = []string{"/dev/null"}
	opts.ErrorOutputPaths = []string{"/dev/null"}
	_ = istiolog.Configure(opts)

	b.ResetTimer()
	var totalLatency time.Duration
	var latencyCount int64

	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		recvChan := make(chan *discovery.DiscoveryRequest, 10)
		sendChan := make(chan *discovery.DiscoveryResponse, 10)
		stream := &mockStream{
			recvChan: recvChan,
			sendChan: sendChan,
			ctx:      ctx,
		}

		con := NewConnection("127.0.0.1:12345", stream)
		mCtx := &mockConnectionContext{
			con:          &con,
			processDelay: 1 * time.Millisecond,
			pushDelay:    20 * time.Millisecond, // Heavy push
		}

		errCh := make(chan error, 1)
		go func() {
			errCh <- Stream(mCtx)
		}()

		// 1. Initial request to trigger firstRequest handling in Receive
		recvChan <- &discovery.DiscoveryRequest{
			TypeUrl: model.ClusterType,
			Node:    &core.Node{Id: "test-node"},
		}

		// Allow connection initialization to complete
		time.Sleep(2 * time.Millisecond)

		// 2. Trigger heavy push
		con.pushChannel <- "push-event"

		// Wait briefly to ensure push is picked up and is blocking
		time.Sleep(1 * time.Millisecond)

		// 3. Send 3 client requests (ACKs) during the push
		for j := 0; j < 3; j++ {
			now := time.Now().UnixNano()
			recvChan <- &discovery.DiscoveryRequest{
				TypeUrl:     model.ClusterType,
				VersionInfo: strconv.FormatInt(now, 10),
			}
		}

		// Wait for the requests to be processed (max 100ms timeout)
		deadline := time.After(100 * time.Millisecond)
		for atomic.LoadInt64(&mCtx.processedCount) < 3 {
			select {
			case <-deadline:
				break
			default:
				time.Sleep(1 * time.Millisecond)
			}
		}

		// Clean up
		cancel()
		<-errCh

		mCtx.latencyMu.Lock()
		for _, lat := range mCtx.latencies {
			totalLatency += lat
			latencyCount++
		}
		mCtx.latencyMu.Unlock()
	}

	if latencyCount > 0 {
		avg := totalLatency / time.Duration(latencyCount)
		b.ReportMetric(float64(avg.Nanoseconds()), "ns/op")
	}
}
