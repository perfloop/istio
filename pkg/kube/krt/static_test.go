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

package krt_test

import (
	"testing"

	"istio.io/istio/pkg/kube/krt"
	"istio.io/istio/pkg/test/util/assert"
)

func TestGetByKeyParts(t *testing.T) {
	opts := testOptions(t)
	first := krt.NewStaticCollection[Named](nil, []Named{{"ns", "first"}}, opts.WithName("first")...)
	second := krt.NewStaticCollection[Named](nil, []Named{{"ns", "second"}}, opts.WithName("second")...)
	derived := krt.NewCollection(first, func(_ krt.HandlerContext, item Named) *Named {
		return &item
	}, opts.WithName("derived")...)
	empty := krt.NewCollection(first, func(_ krt.HandlerContext, _ Named) *Named {
		return nil
	}, opts.WithName("empty")...)
	emptyJoined := krt.JoinCollection([]krt.Collection[Named]{empty}, opts.WithName("empty-joined")...)
	joined := krt.JoinCollection([]krt.Collection[Named]{first, second}, opts.WithName("joined")...)
	present := Named{Namespace: "ns", Name: "present"}
	singleton := krt.NewStatic(&present, true, opts.WithName("singleton")...)
	emptyNamespaceValue := Named{Name: "empty"}
	emptyNamespaceSingleton := krt.NewStatic(&emptyNamespaceValue, true, opts.WithName("empty-namespace-singleton")...)
	emptyNamespace := krt.NewStaticCollection[Named](nil, []Named{emptyNamespaceValue}, opts.WithName("empty-namespace")...)
	emptyNamespaceDerived := krt.NewCollection(emptyNamespace, func(_ krt.HandlerContext, item Named) *Named {
		return &item
	}, opts.WithName("empty-namespace-derived")...)
	assert.EventuallyEqual(t, derived.HasSynced, true)
	assert.EventuallyEqual(t, empty.HasSynced, true)
	assert.EventuallyEqual(t, emptyJoined.HasSynced, true)
	assert.EventuallyEqual(t, joined.HasSynced, true)
	assert.EventuallyEqual(t, emptyNamespaceDerived.HasSynced, true)

	got, found := krt.GetByKeyParts(first, "ns", "first")
	assert.Equal(t, found, true)
	assert.Equal(t, got, Named{"ns", "first"})
	_, found = krt.GetByKeyParts(first, "ns", "missing")
	assert.Equal(t, found, false)
	assert.Equal(t, krt.IsEmpty(first), false)

	got, found = krt.GetByKeyParts(derived, "ns", "first")
	assert.Equal(t, found, true)
	assert.Equal(t, got, Named{"ns", "first"})
	_, found = krt.GetByKeyParts(empty, "ns", "first")
	assert.Equal(t, found, false)
	assert.Equal(t, krt.IsEmpty(emptyJoined), true)

	got, found = krt.GetByKeyParts(joined, "ns", "second")
	assert.Equal(t, found, true)
	assert.Equal(t, got, Named{"ns", "second"})
	assert.Equal(t, krt.IsEmpty(joined), false)

	// Static GetKey ignores its argument, so the generic fallback must verify the returned object's key.
	got, found = krt.GetByKeyParts(singleton.AsCollection(), "ns", "present")
	assert.Equal(t, found, true)
	assert.Equal(t, got, present)
	_, found = krt.GetByKeyParts(singleton.AsCollection(), "ns", "missing")
	assert.Equal(t, found, false)
	_, found = krt.GetByKeyParts(singleton.AsCollection(), "", "ns/present")
	assert.Equal(t, found, false)

	gotEmptyNamespace, found := krt.GetByKeyParts(emptyNamespaceSingleton.AsCollection(), "", "empty")
	assert.Equal(t, found, true)
	assert.Equal(t, gotEmptyNamespace, emptyNamespaceValue)
	gotEmptyNamespace, found = krt.GetByKeyParts(emptyNamespace, "", "empty")
	assert.Equal(t, found, true)
	assert.Equal(t, gotEmptyNamespace, emptyNamespaceValue)
	gotEmptyNamespace, found = krt.GetByKeyParts(emptyNamespaceDerived, "", "empty")
	assert.Equal(t, found, true)
	assert.Equal(t, gotEmptyNamespace, emptyNamespaceValue)
}

func TestStaticCollection(t *testing.T) {
	opts := testOptions(t)
	c := krt.NewStaticCollection[Named](nil, []Named{{"ns", "a"}}, opts.WithName("c")...)
	assert.Equal(t, c.Synced().HasSynced(), true, "should start synced")
	assert.Equal(t, c.List(), []Named{{"ns", "a"}})

	tracker := assert.NewTracker[string](t)
	c.RegisterBatch(BatchedTrackerHandler[Named](tracker), true)
	tracker.WaitOrdered("add/ns/a")

	c.UpdateObject(Named{"ns", "b"})
	tracker.WaitOrdered("add/ns/b")

	c.UpdateObject(Named{"ns", "b"})
	tracker.WaitOrdered("update/ns/b")

	tracker2 := assert.NewTracker[string](t)
	c.RegisterBatch(BatchedTrackerHandler[Named](tracker2), true)
	tracker2.WaitCompare(CompareUnordered("add/ns/a", "add/ns/b"))

	c.DeleteObject("ns/b")
	tracker.WaitOrdered("delete/ns/b")
}
