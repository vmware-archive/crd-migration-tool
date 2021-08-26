// Copyright 2019 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package internal

import (
	"fmt"
	"os"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Options is the set of configurable parameters
// for a Migrator.
type Options struct {
	Resources              string
	LogLevel               string
	Kubeconfig             string
	Context                string
	OldGroupVersion        string
	NewGroupVersion        string
	QPS                    float32
	Burst                  int
	NamespaceMappings      []string
	LabelMappings          []string
	AnnotationMappings     []string
	UpdateOwnerRefMappings []string
}

// Migrator can copy CRD instances from one API group to
// another.
type Migrator struct {
	log                    logrus.FieldLogger
	discoveryClient        discovery.ServerResourcesInterface
	dynamicClient          dynamic.Interface
	oldGroupVersion        schema.GroupVersion
	newGroupVersion        schema.GroupVersion
	crdClient              dynamic.ResourceInterface
	namespaceMappings      map[string]string
	labelMappings          map[string]string
	annotationMappings     map[string]string
	updateOwnerRefMappings map[string]string
	createdItemsTracker    *createdItemsTracker
}

// NewMigrator constructs and returns a *Migrator from
// the provided options.
func NewMigrator(options Options) *Migrator {
	log := newLogger(options.LogLevel)

	restConfig := newRestConfigOrDie(options.Kubeconfig, options.Context)
	restConfig.QPS = options.QPS
	restConfig.Burst = options.Burst

	dynamicClient := dynamic.NewForConfigOrDie(restConfig)
	discoveryClient := discovery.NewDiscoveryClientForConfigOrDie(restConfig)
	oldGroupVersion := parseGroupVersionOrDie(options.OldGroupVersion)
	newGroupVersion := parseGroupVersionOrDie(options.NewGroupVersion)

	crdGroupVersionResource := parseGroupVersionOrDie("apiextensions.k8s.io/v1beta1").WithResource("customresourcedefinitions")
	crdClient := dynamicClient.Resource(crdGroupVersionResource)

	return &Migrator{
		log:                    log,
		discoveryClient:        discoveryClient,
		dynamicClient:          dynamicClient,
		oldGroupVersion:        oldGroupVersion,
		newGroupVersion:        newGroupVersion,
		crdClient:              crdClient,
		namespaceMappings:      parseMappings("namespace", options.NamespaceMappings),
		labelMappings:          parseMappings("label", options.LabelMappings),
		annotationMappings:     parseMappings("annotation", options.AnnotationMappings),
		updateOwnerRefMappings: parseMappings("update-owner-refs", options.UpdateOwnerRefMappings),
		createdItemsTracker:    newCreatedItemsTracker(log, options.OldGroupVersion, options.NewGroupVersion),
	}
}

func newLogger(logLevel string) logrus.FieldLogger {
	log := logrus.New()
	log.Out = os.Stdout
	log.Level = logrus.InfoLevel

	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		log.WithError(err).Error("Error parsing --log-level. Using info instead")
	} else {
		log.Level = level
	}

	return log
}

func parseMappings(kind string, in []string) map[string]string {
	out := make(map[string]string)

	for _, mapping := range in {
		parts := strings.Split(mapping, ":")
		if len(parts) != 2 {
			logrus.Fatalf("invalid %s mapping %q", kind, mapping)
		}
		if parts[0] == "" || parts[1] == "" {
			logrus.Fatalf("invalid %s mapping %q", kind, mapping)
		}

		out[parts[0]] = parts[1]
	}

	return out
}

func calculateResourcePriorities(parentChildMappings map[string]string) ([]string, error) {
	g := newGraph()
	for parent, child := range parentChildMappings {
		g.addEdge(parent, child)
	}
	return g.sort()
}

// MigrateAllResources copies all instances of all resources within the
// old group/version to the new, applying any relevant mappings.
func (m *Migrator) MigrateAllResources() {
	serverResources, err := m.discoveryClient.ServerResourcesForGroupVersion(m.oldGroupVersion.String())
	if err != nil {
		m.log.WithError(err).Fatal("Error retrieving server resources for old group version")
	}

	serverResourcesByName := map[string]metav1.APIResource{}

	for _, resource := range serverResources.APIResources {
		serverResourcesByName[resource.Name] = resource
	}

	resourcePriorities, err := calculateResourcePriorities(m.updateOwnerRefMappings)
	if err != nil {
		m.log.Fatal("--update-owner-refs contains a cycle")
	}

	// check all the --update-owner-refs values to make sure they're valid; if not, error now, before
	// doing any real work.
	for _, resourceName := range resourcePriorities {
		if _, found := serverResourcesByName[resourceName]; !found {
			m.log.Fatalf("unable to find resource %q from --update-owner-refs", resourceName)
		}
	}

	// process the sorted list of prioritized resources from --update-owner-refs first
	for _, resourceName := range resourcePriorities {
		resource := serverResourcesByName[resourceName]

		// if it's a parent, register it
		if _, ok := m.updateOwnerRefMappings[resourceName]; ok {
			m.createdItemsTracker.registerResource(resource)
		}

		m.migrateOneResource(resource)

		// delete the resource from the map so we won't process it again in the 2nd for loop
		delete(serverResourcesByName, resourceName)
	}

	// process any remaining resources not listed in --update-owner-refs
	for _, resource := range serverResourcesByName {
		m.migrateOneResource(resource)
	}
}

func (m *Migrator) MigrateSomeResources(resourceSet stringSet) {
	serverResources, err := m.discoveryClient.ServerResourcesForGroupVersion(m.oldGroupVersion.String())
	if err != nil {
		m.log.WithError(err).Fatal("Error retrieving server resources for old group version")
	}

	serverResourcesByName := map[string]metav1.APIResource{}

	for _, resource := range serverResources.APIResources {
		serverResourcesByName[resource.Name] = resource
	}

	resourcePriorities, err := calculateResourcePriorities(m.updateOwnerRefMappings)
	if err != nil {
		m.log.Fatal("--update-owner-refs contains a cycle")
	}

	// check all the --update-owner-refs values to make sure they're valid; if not, error now, before
	// doing any real work.
	for _, resourceName := range resourcePriorities {
		if _, found := serverResourcesByName[resourceName]; !found {
			m.log.Fatalf("unable to find resource %q from --update-owner-refs", resourceName)
		}
	}

	// process the sorted list of prioritized resources from --update-owner-refs first
	for _, resourceName := range resourcePriorities {
		resource := serverResourcesByName[resourceName]

		// if it's a parent, register it
		if _, ok := m.updateOwnerRefMappings[resourceName]; ok {
			m.createdItemsTracker.registerResource(resource)
		}
		if nil == resourceSet || resourceSet.has(resourceName) {
			m.migrateOneResource(resource)
		}

		// delete the resource from the map so we won't process it again in the 2nd for loop
		delete(serverResourcesByName, resourceName)
	}

	// process any remaining resources not listed in --update-owner-refs
	for _, resource := range serverResourcesByName {
		if nil == resourceSet || resourceSet.has(resource.Name) {
			m.migrateOneResource(resource)
		}
	}
}

func (m *Migrator) migrateOneResource(resource metav1.APIResource) {
	log := m.log.WithField("resource", resource.Name)

	log.Info("Starting resource migration")

	if err := m.validateNewCRD(log, resource); err != nil {
		log.WithError(err).Error("Unable to migrate resource")
		return
	}

	defer log.Info("Completed resource migration")

	oldGVR := m.oldGroupVersion.WithResource(resource.Name)

	oldClient := m.dynamicClient.Resource(oldGVR)
	list, err := oldClient.List(metav1.ListOptions{})
	if err != nil {
		log.WithError(err).Error("Unable to list items")
		return
	}

	for _, item := range list.Items {
		if err := m.migrateOneResourceInstance(log, resource.Name, &item); err != nil {
			log.WithError(err).Error("Error migrating item")
		}
	}
}

func (m *Migrator) validateNewCRD(log logrus.FieldLogger, resource metav1.APIResource) error {
	crdName := fmt.Sprintf("%s.%s", resource.Name, m.newGroupVersion.Group)
	crd, err := m.crdClient.Get(crdName, metav1.GetOptions{})
	if err != nil {
		return errors.WithStack(err)
	}

	_, exists, _ := unstructured.NestedMap(crd.Object, "spec", "subresources", "status")
	if exists {
		return errors.Errorf("CRD %s has spec.subresources.status", crdName)
	}

	return nil
}

func (m *Migrator) migrateOneResourceInstance(logger logrus.FieldLogger, resourceName string, item *unstructured.Unstructured) error {
	newGVR := m.newGroupVersion.WithResource(resourceName)
	originalNS := item.GetNamespace()
	targetNS := m.getTargetNamespace(originalNS)
	newResourceClient := clientForItem(m.dynamicClient.Resource(newGVR), targetNS)

	// set up the log fields
	var id string
	if targetNS != "" {
		id = targetNS + "/" + item.GetName()
	} else {
		id = item.GetName()
	}
	log := logger.WithField("id", id)
	if originalNS != targetNS {
		log = log.WithField("original-namespace", originalNS)
	}

	log.Info("Checking if item already exists in new API group")
	existingItem, err := newResourceClient.Get(item.GetName(), metav1.GetOptions{})
	if err == nil {
		log.Warn("Item already exists - skipping")

		// need to track the item in case it's a parent and we need to update its UID in child ownerRefs
		m.createdItemsTracker.registerCreatedItem(existingItem)

		return nil
	} else if !apierrors.IsNotFound(err) {
		return errors.WithStack(err)
	}

	m.prepareForCreate(log, item)

	log.Info("Creating item")
	createdItem, err := newResourceClient.Create(item, metav1.CreateOptions{})
	if err != nil {
		return errors.WithStack(err)
	}

	m.createdItemsTracker.registerCreatedItem(createdItem)

	return nil
}

func (m *Migrator) prepareForCreate(log logrus.FieldLogger, item *unstructured.Unstructured) {
	// Change apiVersion to the new one
	item.SetAPIVersion(m.newGroupVersion.String())

	// Have to clear out resourceVersion to be able to create
	item.SetResourceVersion("")

	item.SetNamespace(m.getTargetNamespace(item.GetNamespace()))

	if len(m.annotationMappings) > 0 {
		log.Debug("Updating annotation keys")
		item.SetAnnotations(updateMapKeys(item.GetAnnotations(), m.annotationMappings))
	}

	if len(m.labelMappings) > 0 {
		log.Debug("Updating label keys")
		item.SetLabels(updateMapKeys(item.GetLabels(), m.labelMappings))
	}

	m.createdItemsTracker.updateOwnerRefs(item)
}

func updateMapKeys(data, mappings map[string]string) map[string]string {
	for key, value := range data {
		for find, replace := range mappings {
			if updatedKey := strings.Replace(key, find, replace, -1); updatedKey != key {
				data[updatedKey] = value
				delete(data, key)
			}
		}
	}

	return data
}

func newRestConfigOrDie(kubeconfig, context string) *rest.Config {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.ExplicitPath = kubeconfig

	configOverrides := &clientcmd.ConfigOverrides{
		CurrentContext: context,
	}

	clientcmdClientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := clientcmdClientConfig.ClientConfig()
	if err != nil {
		panic(err)
	}

	return config
}

func parseGroupVersionOrDie(groupVersion string) schema.GroupVersion {
	parsed, err := schema.ParseGroupVersion(groupVersion)
	if err != nil {
		logrus.WithError(err).Fatalf("Error parsing groupVersion %s", groupVersion)
	}

	return parsed
}

func clientForItem(namespaceableClient dynamic.NamespaceableResourceInterface, namespace string) dynamic.ResourceInterface {
	if namespace != "" {
		return namespaceableClient.Namespace(namespace)
	}
	return namespaceableClient
}

func (m *Migrator) getTargetNamespace(original string) string {
	target, found := m.namespaceMappings[original]
	if found {
		return target
	}
	return original
}
