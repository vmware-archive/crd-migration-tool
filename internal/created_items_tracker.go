// Copyright 2019 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package internal

import (
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

type createdItemsTracker struct {
	log                logrus.FieldLogger
	oldGroupVersion    string
	newGroupVersion    string
	resourcesByKind    map[string]metav1.APIResource
	createdItemsByKind map[string]*createdItems
}

func newCreatedItemsTracker(log logrus.FieldLogger, oldGroupVersion, newGroupVersion string) *createdItemsTracker {
	return &createdItemsTracker{
		log:                log,
		oldGroupVersion:    oldGroupVersion,
		newGroupVersion:    newGroupVersion,
		resourcesByKind:    make(map[string]metav1.APIResource),
		createdItemsByKind: make(map[string]*createdItems),
	}
}

func (c *createdItemsTracker) registerResource(resource metav1.APIResource) {
	c.log.WithField("kind", resource.Kind).Debug("Registering resource for ownerRef tracking")
	c.resourcesByKind[resource.Kind] = resource
	c.createdItemsByKind[resource.Kind] = newCreatedItems()
}

func (c *createdItemsTracker) registerCreatedItem(item *unstructured.Unstructured) {
	byKind, ok := c.createdItemsByKind[item.GetKind()]
	if !ok {
		c.log.WithFields(logrus.Fields{
			"kind": item.GetKind(),
			"name": item.GetName(),
		}).Debug("Not tracking item because it's not listed as a possible ownerRef parent")

		return
	}

	byKind.registerCreatedItem(item)
}

func (c *createdItemsTracker) updateOwnerRefs(item *unstructured.Unstructured) {
	var updatedOwnerRefs []metav1.OwnerReference
	for _, ownerRef := range item.GetOwnerReferences() {
		log := c.log.WithFields(logrus.Fields{
			"ownerRef.kind": ownerRef.Kind,
			"ownerRef.name": ownerRef.Name,
		})

		if ownerRef.APIVersion != c.oldGroupVersion {
			log.Debug("ownerRef's apiVersion is not the one being migrated, not updating")
			updatedOwnerRefs = append(updatedOwnerRefs, ownerRef)
			continue
		}

		byKind := c.createdItemsByKind[ownerRef.Kind]
		if byKind == nil {
			log.Debug("ownerRef's kind is not being tracked, not updating")
			updatedOwnerRefs = append(updatedOwnerRefs, ownerRef)
			continue
		}

		createdItem, ok := byKind.getByName(ownerRef.Name)
		if !ok {
			log.Warn("Unable to update ownerRef because owner was not migrated by this tool")
			updatedOwnerRefs = append(updatedOwnerRefs, ownerRef)
			continue
		}

		log.Info("Updating ownerRef's apiVersion and UID")
		ownerRef.APIVersion = c.newGroupVersion
		ownerRef.UID = createdItem.uid

		updatedOwnerRefs = append(updatedOwnerRefs, ownerRef)
	}

	item.SetOwnerReferences(updatedOwnerRefs)
}

type createdItems struct {
	items map[string]itemInfo
}

func newCreatedItems() *createdItems {
	return &createdItems{
		items: make(map[string]itemInfo),
	}
}

func (c *createdItems) registerCreatedItem(item *unstructured.Unstructured) {
	c.items[item.GetName()] = newItemInfo(item)
}

func (c *createdItems) getByName(name string) (itemInfo, bool) {
	i, ok := c.items[name]
	return i, ok
}

type itemInfo struct {
	name string
	uid  types.UID
}

func newItemInfo(item *unstructured.Unstructured) itemInfo {
	return itemInfo{
		name: item.GetName(),
		uid:  item.GetUID(),
	}
}
