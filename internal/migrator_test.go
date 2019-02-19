// Copyright 2019 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package internal

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

type migratorHarness struct {
	t               *testing.T
	migrator        *Migrator
	discoveryClient *fakediscovery.FakeDiscovery
	dynamicClient   *fakedynamic.FakeDynamicClient
}

func newHarness(
	t *testing.T,
	oldGV, newGV schema.GroupVersion,
	nsMappings, labelMappings, annotationMappings, updateOwnerRefMappings map[string]string,
) *migratorHarness {
	logger := logrus.New()
	logger.Level = logrus.DebugLevel

	discoveryClient := &fakediscovery.FakeDiscovery{Fake: new(k8stesting.Fake)}
	dynamicClient := fakedynamic.NewSimpleDynamicClient(runtime.NewScheme())

	crdGroupVersionResource := parseGroupVersionOrDie("apiextensions.k8s.io/v1beta1").WithResource("customresourcedefinitions")
	crdClient := dynamicClient.Resource(crdGroupVersionResource)

	migrator := &Migrator{
		log:                    logger,
		discoveryClient:        discoveryClient,
		dynamicClient:          dynamicClient,
		oldGroupVersion:        oldGV,
		newGroupVersion:        newGV,
		crdClient:              crdClient,
		createdItemsTracker:    newCreatedItemsTracker(logger, oldGV.String(), newGV.String()),
		namespaceMappings:      nsMappings,
		labelMappings:          labelMappings,
		annotationMappings:     annotationMappings,
		updateOwnerRefMappings: updateOwnerRefMappings,
	}

	return &migratorHarness{
		t:               t,
		migrator:        migrator,
		discoveryClient: discoveryClient,
		dynamicClient:   dynamicClient,
	}
}

func (h *migratorHarness) RegisterCRD(gvr schema.GroupVersionResource) {
	var gvList *metav1.APIResourceList

	for _, resourceList := range h.discoveryClient.Resources {
		if resourceList.GroupVersion == gvr.GroupVersion().String() {
			gvList = resourceList
			break
		}
	}

	if gvList == nil {
		gvList = &metav1.APIResourceList{GroupVersion: gvr.GroupVersion().String()}
		h.discoveryClient.Resources = append(h.discoveryClient.Resources, gvList)
	}

	gvList.APIResources = append(gvList.APIResources, metav1.APIResource{Name: gvr.Resource, Kind: strings.Title(gvr.Resource)})

	crd := new(unstructured.Unstructured)
	crd.SetName(fmt.Sprintf("%s.%s", gvr.Resource, gvr.Group))

	_, err := h.migrator.crdClient.Create(crd, metav1.CreateOptions{})
	require.NoError(h.t, err)
}

func (h *migratorHarness) AddResources(gvr schema.GroupVersionResource, objs ...*unstructured.Unstructured) {
	client := h.dynamicClient.Resource(gvr)

	for _, obj := range objs {
		var err error
		if ns := obj.GetNamespace(); ns != "" {
			_, err = client.Namespace(ns).Create(obj, metav1.CreateOptions{})
		} else {
			_, err = client.Create(obj, metav1.CreateOptions{})
		}

		require.NoError(h.t, err)
	}
}

type unstructuredBuilder struct {
	*unstructured.Unstructured
}

func objectBuilder(apiVersion, kind, name string) *unstructuredBuilder {
	b := &unstructuredBuilder{Unstructured: new(unstructured.Unstructured)}

	b.SetAPIVersion(apiVersion)
	b.SetKind(kind)
	b.SetName(name)

	return b
}

func (b *unstructuredBuilder) Namespace(val string) *unstructuredBuilder {
	b.SetNamespace(val)
	return b
}

func (b *unstructuredBuilder) Labels(val map[string]string) *unstructuredBuilder {
	b.SetLabels(val)
	return b
}

func (b *unstructuredBuilder) Annotations(val map[string]string) *unstructuredBuilder {
	b.SetAnnotations(val)
	return b
}

func (b *unstructuredBuilder) Annotation(k, v string) *unstructuredBuilder {
	annotations := b.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[k] = v
	b.SetAnnotations(annotations)
	return b
}

func (b *unstructuredBuilder) OwnerRef(apiVersion, kind, name string) *unstructuredBuilder {
	b.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: apiVersion,
			Kind:       kind,
			Name:       name,
		},
	})
	return b
}

func (b *unstructuredBuilder) Build() *unstructured.Unstructured {
	return b.Unstructured
}

func TestMigrate(t *testing.T) {
	tests := []struct {
		name                   string
		fromGV                 schema.GroupVersion
		toGV                   schema.GroupVersion
		namespaceMappings      map[string]string
		labelMappings          map[string]string
		annotationMappings     map[string]string
		updateOwnerRefMappings map[string]string
		fromGVResources        map[string][]*unstructured.Unstructured
		toGVResources          map[string][]*unstructured.Unstructured
		want                   map[string][]unstructured.Unstructured
	}{
		{
			name:   "cluster-scoped resource with no mappings",
			fromGV: schema.GroupVersion{Group: "old", Version: "v1"},
			toGV:   schema.GroupVersion{Group: "new", Version: "v1"},
			fromGVResources: map[string][]*unstructured.Unstructured{
				"foo": []*unstructured.Unstructured{
					objectBuilder("old/v1", "Foo", "obj-1").Build(),
					objectBuilder("old/v1", "Foo", "obj-2").Build(),
				},
			},
			toGVResources: map[string][]*unstructured.Unstructured{
				"foo": nil,
			},
			want: map[string][]unstructured.Unstructured{
				"foo": []unstructured.Unstructured{
					*objectBuilder("new/v1", "Foo", "obj-1").Build(),
					*objectBuilder("new/v1", "Foo", "obj-2").Build(),
				},
			},
		},
		{
			name:   "namespaced resource with no mappings",
			fromGV: schema.GroupVersion{Group: "old", Version: "v1"},
			toGV:   schema.GroupVersion{Group: "new", Version: "v1"},
			fromGVResources: map[string][]*unstructured.Unstructured{
				"foo": []*unstructured.Unstructured{
					objectBuilder("old/v1", "Foo", "obj-1").Namespace("ns-1").Build(),
					objectBuilder("old/v1", "Foo", "obj-2").Namespace("ns-1").Build(),
				},
			},
			toGVResources: map[string][]*unstructured.Unstructured{
				"foo": nil,
			},
			want: map[string][]unstructured.Unstructured{
				"foo": []unstructured.Unstructured{
					*objectBuilder("new/v1", "Foo", "obj-1").Namespace("ns-1").Build(),
					*objectBuilder("new/v1", "Foo", "obj-2").Namespace("ns-1").Build(),
				},
			},
		},
		{
			name:   "cluster-scoped and namespaced resources with no mappings",
			fromGV: schema.GroupVersion{Group: "old", Version: "v1"},
			toGV:   schema.GroupVersion{Group: "new", Version: "v1"},
			fromGVResources: map[string][]*unstructured.Unstructured{
				"foo": []*unstructured.Unstructured{
					objectBuilder("old/v1", "Foo", "obj-1").Build(),
					objectBuilder("old/v1", "Foo", "obj-2").Build(),
				},
				"bar": []*unstructured.Unstructured{
					objectBuilder("old/v1", "Bar", "obj-1").Namespace("ns-1").Build(),
					objectBuilder("old/v1", "Bar", "obj-2").Namespace("ns-2").Build(),
				},
			},
			toGVResources: map[string][]*unstructured.Unstructured{
				"foo": nil,
				"bar": nil,
			},
			want: map[string][]unstructured.Unstructured{
				"foo": []unstructured.Unstructured{
					*objectBuilder("new/v1", "Foo", "obj-1").Build(),
					*objectBuilder("new/v1", "Foo", "obj-2").Build(),
				},
				"bar": []unstructured.Unstructured{
					*objectBuilder("new/v1", "Bar", "obj-1").Namespace("ns-1").Build(),
					*objectBuilder("new/v1", "Bar", "obj-2").Namespace("ns-2").Build(),
				},
			},
		},
		{
			name:   "namespaced resource with namespace mappings",
			fromGV: schema.GroupVersion{Group: "old", Version: "v1"},
			toGV:   schema.GroupVersion{Group: "new", Version: "v1"},
			fromGVResources: map[string][]*unstructured.Unstructured{
				"foo": []*unstructured.Unstructured{
					objectBuilder("old/v1", "Foo", "obj-1").Namespace("old-ns-1").Build(),
					objectBuilder("old/v1", "Foo", "obj-2").Namespace("old-ns-2").Build(),
				},
			},
			toGVResources: map[string][]*unstructured.Unstructured{
				"foo": nil,
			},
			namespaceMappings: map[string]string{
				"old-ns-1": "new-ns-1",
				"old-ns-2": "new-ns-2",
			},
			want: map[string][]unstructured.Unstructured{
				"foo": []unstructured.Unstructured{
					*objectBuilder("new/v1", "Foo", "obj-1").Namespace("new-ns-1").Build(),
					*objectBuilder("new/v1", "Foo", "obj-2").Namespace("new-ns-2").Build(),
				},
			},
		},
		{
			name:   "object that already exists in the target GV is not migrated",
			fromGV: schema.GroupVersion{Group: "old", Version: "v1"},
			toGV:   schema.GroupVersion{Group: "new", Version: "v1"},
			fromGVResources: map[string][]*unstructured.Unstructured{
				"foo": []*unstructured.Unstructured{
					objectBuilder("old/v1", "Foo", "obj-1").Namespace("ns-1").Annotation("from-source", "true").Build(),
					objectBuilder("old/v1", "Foo", "obj-2").Namespace("ns-1").Annotation("from-source", "true").Build(),
				},
			},
			toGVResources: map[string][]*unstructured.Unstructured{
				"foo": []*unstructured.Unstructured{
					objectBuilder("new/v1", "Foo", "obj-1").Namespace("ns-1").Annotation("from-source", "false").Build(),
				},
			},
			want: map[string][]unstructured.Unstructured{
				"foo": []unstructured.Unstructured{
					*objectBuilder("new/v1", "Foo", "obj-1").Namespace("ns-1").Annotation("from-source", "false").Build(),
					*objectBuilder("new/v1", "Foo", "obj-2").Namespace("ns-1").Annotation("from-source", "true").Build(),
				},
			},
		},
		{
			name:   "object that already exists in the target GV in a mapped namespace is not migrated",
			fromGV: schema.GroupVersion{Group: "old", Version: "v1"},
			toGV:   schema.GroupVersion{Group: "new", Version: "v1"},
			fromGVResources: map[string][]*unstructured.Unstructured{
				"foo": []*unstructured.Unstructured{
					objectBuilder("old/v1", "Foo", "obj-1").Namespace("ns-1").Annotation("from-source", "true").Build(),
					objectBuilder("old/v1", "Foo", "obj-2").Namespace("ns-1").Annotation("from-source", "true").Build(),
				},
			},
			toGVResources: map[string][]*unstructured.Unstructured{
				"foo": []*unstructured.Unstructured{
					objectBuilder("new/v1", "Foo", "obj-1").Namespace("ns-2").Annotation("from-source", "false").Build(),
				},
			},
			namespaceMappings: map[string]string{
				"ns-1": "ns-2",
			},
			want: map[string][]unstructured.Unstructured{
				"foo": []unstructured.Unstructured{
					*objectBuilder("new/v1", "Foo", "obj-1").Namespace("ns-2").Annotation("from-source", "false").Build(),
					*objectBuilder("new/v1", "Foo", "obj-2").Namespace("ns-2").Annotation("from-source", "true").Build(),
				},
			},
		},
		{
			name:   "namespaced resource with label mappings",
			fromGV: schema.GroupVersion{Group: "old", Version: "v1"},
			toGV:   schema.GroupVersion{Group: "new", Version: "v1"},
			fromGVResources: map[string][]*unstructured.Unstructured{
				"foo": []*unstructured.Unstructured{
					objectBuilder("old/v1", "Foo", "obj-1").Namespace("ns-1").
						Labels(map[string]string{
							"foo.io":     "a-val",
							"foo.io/a":   "another-val",
							"a.baz.io":   "yet-another-val",
							"no-map-key": "y",
						}).Build(),
					objectBuilder("old/v1", "Foo", "obj-2").Namespace("ns-1").
						Labels(map[string]string{
							"baz.io":   "value-1",
							"baz.io/a": "value-2",
							"a.foo.io": "value-3",
							"no-map":   "value-4",
						}).Build(),
				},
			},
			toGVResources: map[string][]*unstructured.Unstructured{
				"foo": nil,
			},
			labelMappings: map[string]string{
				"foo.io": "bar.io",
				"baz.io": "zoo.io",
			},
			want: map[string][]unstructured.Unstructured{
				"foo": []unstructured.Unstructured{
					*objectBuilder("new/v1", "Foo", "obj-1").Namespace("ns-1").
						Labels(map[string]string{
							"bar.io":     "a-val",
							"bar.io/a":   "another-val",
							"a.zoo.io":   "yet-another-val",
							"no-map-key": "y",
						}).Build(),
					*objectBuilder("new/v1", "Foo", "obj-2").Namespace("ns-1").
						Labels(map[string]string{
							"zoo.io":   "value-1",
							"zoo.io/a": "value-2",
							"a.bar.io": "value-3",
							"no-map":   "value-4",
						}).Build(),
				},
			},
		},
		{
			name:   "namespaced resource with annotation mappings",
			fromGV: schema.GroupVersion{Group: "old", Version: "v1"},
			toGV:   schema.GroupVersion{Group: "new", Version: "v1"},
			fromGVResources: map[string][]*unstructured.Unstructured{
				"foo": []*unstructured.Unstructured{
					objectBuilder("old/v1", "Foo", "obj-1").Namespace("ns-1").
						Annotations(map[string]string{
							"foo.io":     "a-val",
							"foo.io/a":   "another-val",
							"a.baz.io":   "yet-another-val",
							"no-map-key": "y",
						}).Build(),
					objectBuilder("old/v1", "Foo", "obj-2").Namespace("ns-1").
						Annotations(map[string]string{
							"baz.io":   "value-1",
							"baz.io/a": "value-2",
							"a.foo.io": "value-3",
							"no-map":   "value-4",
						}).Build(),
				},
			},
			toGVResources: map[string][]*unstructured.Unstructured{
				"foo": nil,
			},
			annotationMappings: map[string]string{
				"foo.io": "bar.io",
				"baz.io": "zoo.io",
			},
			want: map[string][]unstructured.Unstructured{
				"foo": []unstructured.Unstructured{
					*objectBuilder("new/v1", "Foo", "obj-1").Namespace("ns-1").
						Annotations(map[string]string{
							"bar.io":     "a-val",
							"bar.io/a":   "another-val",
							"a.zoo.io":   "yet-another-val",
							"no-map-key": "y",
						}).Build(),
					*objectBuilder("new/v1", "Foo", "obj-2").Namespace("ns-1").
						Annotations(map[string]string{
							"zoo.io":   "value-1",
							"zoo.io/a": "value-2",
							"a.bar.io": "value-3",
							"no-map":   "value-4",
						}).Build(),
				},
			},
		},
		{
			name:   "namespaced resources with owner refs",
			fromGV: schema.GroupVersion{Group: "old", Version: "v1"},
			toGV:   schema.GroupVersion{Group: "new", Version: "v1"},
			fromGVResources: map[string][]*unstructured.Unstructured{
				"foo": []*unstructured.Unstructured{
					objectBuilder("old/v1", "Foo", "obj-1").Namespace("ns-1").OwnerRef("old/v1", "Bar", "obj-1").Build(),
					objectBuilder("old/v1", "Foo", "obj-2").Namespace("ns-1").OwnerRef("old/v1", "Baz", "obj-1").Build(),
					objectBuilder("old/v1", "Foo", "obj-3").Namespace("ns-1").OwnerRef("altgroup/v1", "Blue", "obj-1").Build(),
				},
				"bar": []*unstructured.Unstructured{
					objectBuilder("old/v1", "Bar", "obj-1").Namespace("ns-1").Build(),
				},
				"baz": []*unstructured.Unstructured{
					objectBuilder("old/v1", "Baz", "obj-1").Namespace("ns-1").Build(),
				},
			},
			toGVResources: map[string][]*unstructured.Unstructured{
				"foo": nil,
				"bar": nil,
				"baz": nil,
			},
			updateOwnerRefMappings: map[string]string{
				"bar": "foo",
				"baz": "foo",
			},
			want: map[string][]unstructured.Unstructured{
				"foo": []unstructured.Unstructured{
					*objectBuilder("new/v1", "Foo", "obj-1").Namespace("ns-1").OwnerRef("new/v1", "Bar", "obj-1").Build(),
					*objectBuilder("new/v1", "Foo", "obj-2").Namespace("ns-1").OwnerRef("new/v1", "Baz", "obj-1").Build(),
					*objectBuilder("new/v1", "Foo", "obj-3").Namespace("ns-1").OwnerRef("altgroup/v1", "Blue", "obj-1").Build(),
				},
				"bar": []unstructured.Unstructured{
					*objectBuilder("new/v1", "Bar", "obj-1").Namespace("ns-1").Build(),
				},
				"baz": []unstructured.Unstructured{
					*objectBuilder("new/v1", "Baz", "obj-1").Namespace("ns-1").Build(),
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.fromGV, tc.toGV, tc.namespaceMappings, tc.labelMappings, tc.annotationMappings, tc.updateOwnerRefMappings)

			for resource, items := range tc.fromGVResources {
				gvr := tc.fromGV.WithResource(resource)

				h.RegisterCRD(gvr)
				h.AddResources(gvr, items...)
			}
			for resource, items := range tc.toGVResources {
				gvr := tc.toGV.WithResource(resource)

				h.RegisterCRD(gvr)
				h.AddResources(gvr, items...)
			}

			h.migrator.MigrateAllResources()

			for resource, items := range tc.want {
				client := h.dynamicClient.Resource(tc.toGV.WithResource(resource))

				res, err := client.List(metav1.ListOptions{})
				require.NoError(t, err)

				assert.Equal(t, items, res.Items)
			}
		})
	}
}

func TestUpdateMapKeys(t *testing.T) {
	tests := []struct {
		name               string
		original, expected map[string]string
	}{
		{
			name:     "nil map",
			original: nil,
			expected: nil,
		},
		{
			name:     "empty map",
			original: map[string]string{},
			expected: map[string]string{},
		},
		{
			name:     "no matches",
			original: map[string]string{"a": "b", "c": "d"},
			expected: map[string]string{"a": "b", "c": "d"},
		},
		{
			name:     "some matches",
			original: map[string]string{"a": "b", "c": "d", "widget.foo.example.com/color": "blue", "fromble.foo.example.com/shape": "circle"},
			expected: map[string]string{"a": "b", "c": "d", "widget.bar.io/color": "blue", "fromble.bar.io/shape": "circle"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updated := updateMapKeys(tt.original, map[string]string{"foo.example.com": "bar.io"})
			assert.Equal(t, tt.expected, updated)
		})
	}
}

func TestPrepareForCreate(t *testing.T) {
	item := unstructuredOrDie(t, `
	{
		"apiVersion": "my.example.com/v1",
		"kind": "Foo",
		"metadata": {
			"namespace": "example",
			"name": "foo1",
			"labels": {
				"pre.my.example.com/color": "green",
				"my.example.com/shape": "square"
			},
			"annotations": {
				"pre.my.example.com/color": "blue",
				"my.example.com/shape": "circle"
			},
			"resourceVersion": "1234"
		},
		"spec": {
			"someField": "someValue"
		}
	}`)

	assert.Equal(t, "my.example.com/v1", item.GetAPIVersion())
	assert.Equal(t, "Foo", item.GetKind())
	assert.Equal(t, "example", item.GetNamespace())
	assert.Equal(t, "foo1", item.GetName())
	originalLabels := map[string]string{
		"pre.my.example.com/color": "green",
		"my.example.com/shape":     "square",
	}
	assert.Equal(t, originalLabels, item.GetLabels())
	originalAnnotations := map[string]string{
		"pre.my.example.com/color": "blue",
		"my.example.com/shape":     "circle",
	}
	assert.Equal(t, originalAnnotations, item.GetAnnotations())
	assert.Equal(t, "1234", item.GetResourceVersion())
	spec := map[string]interface{}{
		"someField": "someValue",
	}
	assert.Equal(t, spec, item.Object["spec"])

	m := &Migrator{
		newGroupVersion:    schema.GroupVersion{Group: "example.io", Version: "v1"},
		labelMappings:      map[string]string{"my.example.com": "example.io"},
		annotationMappings: map[string]string{"my.example.com": "example.io"},
		namespaceMappings:  map[string]string{"example": "other"},
	}

	logger := logrus.New()
	logger.Out = ioutil.Discard
	log := logrus.NewEntry(logger)
	m.prepareForCreate(log, item)

	assert.Equal(t, "example.io/v1", item.GetAPIVersion())
	assert.Equal(t, "Foo", item.GetKind())
	assert.Equal(t, "other", item.GetNamespace())
	assert.Equal(t, "foo1", item.GetName())
	updatedLabels := map[string]string{
		"pre.example.io/color": "green",
		"example.io/shape":     "square",
	}
	assert.Equal(t, updatedLabels, item.GetLabels())
	updatedAnnotations := map[string]string{
		"pre.example.io/color": "blue",
		"example.io/shape":     "circle",
	}
	assert.Equal(t, updatedAnnotations, item.GetAnnotations())
	assert.Empty(t, item.GetResourceVersion())
	assert.Equal(t, spec, item.Object["spec"])
}

func unstructuredOrDie(t *testing.T, s string) *unstructured.Unstructured {
	var u unstructured.Unstructured
	if err := json.Unmarshal([]byte(s), &u); err != nil {
		t.Fatalf("error unmarshaling json: %v", err)
	}
	return &u
}

type fakeNamespaceableResourceClient struct {
	dynamic.NamespaceableResourceInterface
	invokedNamespace string
}

func (f *fakeNamespaceableResourceClient) Namespace(namespace string) dynamic.ResourceInterface {
	f.invokedNamespace = namespace
	return &fakeNamespacedResourceClient{}
}

type fakeNamespacedResourceClient struct {
	dynamic.ResourceInterface
}

func TestClientForItem(t *testing.T) {
	f := &fakeNamespaceableResourceClient{}

	c := clientForItem(f, "")
	assert.Equal(t, f, c)
	assert.Empty(t, f.invokedNamespace)

	c = clientForItem(f, "some-namespace")
	assert.NotEqual(t, f, c)
	assert.Equal(t, "some-namespace", f.invokedNamespace)
	assert.Implements(t, (*dynamic.ResourceInterface)(nil), c)
}

func TestGetTargetNamespace(t *testing.T) {
	m := &Migrator{
		namespaceMappings: map[string]string{"a": "b"},
	}

	assert.Equal(t, "notfound", m.getTargetNamespace("notfound"))
	assert.Equal(t, "b", m.getTargetNamespace("a"))
}

func TestParseGroupVersionOrDie(t *testing.T) {
	originalExitFunc := logrus.StandardLogger().ExitFunc
	defer func() {
		logrus.StandardLogger().ExitFunc = originalExitFunc
	}()

	logrus.StandardLogger().ExitFunc = func(code int) {
		panic(code)
	}

	parseFunc := func(gv string) func() {
		return func() {
			parseGroupVersionOrDie(gv)
		}
	}

	assert.Panics(t, parseFunc("a/b/c"))

	parsed := parseGroupVersionOrDie("example.com/v1")
	assert.Equal(t, "example.com", parsed.Group)
	assert.Equal(t, "v1", parsed.Version)
}

func TestParseMappings(t *testing.T) {
	originalExitFunc := logrus.StandardLogger().ExitFunc
	defer func() {
		logrus.StandardLogger().ExitFunc = originalExitFunc
	}()

	logrus.StandardLogger().ExitFunc = func(code int) {
		panic(code)
	}

	assert.Panics(t, func() {
		parseMappings("foo", []string{"asdf"})
	})
	assert.Panics(t, func() {
		parseMappings("foo", []string{":asdf"})
	})
	assert.Panics(t, func() {
		parseMappings("foo", []string{"asdf:"})
	})

	mappings := parseMappings("foo", []string{})
	assert.Empty(t, mappings)

	mappings = parseMappings("foo", []string{"a:b"})
	assert.Equal(t, map[string]string{"a": "b"}, mappings)

	mappings = parseMappings("foo", []string{"a:b", "c:d"})
	assert.Equal(t, map[string]string{"a": "b", "c": "d"}, mappings)
}
